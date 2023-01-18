package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/fsnotify/fsnotify"
	netlinklistlistens "github.com/yskszk63/netlink-list-listens"
	"golang.org/x/crypto/ssh"
)

type env struct {
	sockPath          string
	hostkeyPath       string
	authorizedKeysDir string
}

func sshKeygen() ([]byte, ssh.Signer, error) {
	publickey, privatekey, err := ed25519.GenerateKey(nil)
	if err != nil {
		return nil, nil, err
	}

	sshkey, err := ssh.NewSignerFromKey(privatekey)
	if err != nil {
		return nil, nil, err
	}

	pub, err := ssh.NewPublicKey(publickey)
	if err != nil {
		return nil, nil, err
	}

	return ssh.MarshalAuthorizedKey(pub), sshkey, nil
}

func hostkey(hostkeyPath string) (ssh.HostKeyCallback, error) {
	raw, err := os.ReadFile(hostkeyPath)
	if err != nil {
		return nil, err
	}
	key, _, _, _, err := ssh.ParseAuthorizedKey(raw)
	if err != nil {
		return nil, err
	}
	knownhosts := ssh.FixedHostKey(key)
	return knownhosts, err
}

func storePublickey(dir string, k []byte) (string, error) {
	try := 0
	for {
		var b [4]byte
		if _, err := rand.Read(b[:]); err != nil {
			return "", err
		}
		name := fmt.Sprintf("%08x.pub", *(*uint32)(unsafe.Pointer(&b)))

		fp, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if os.IsExist(err) {
			if try++; try < 100 {
				continue
			}
			return "", err
		}
		if err != nil {
			return "", err
		}
		defer fp.Close()

		if _, err := fp.Write(k); err != nil {
			return "", err
		}
		return fp.Name(), nil
	}
}

func updateListens(m *map[netip.AddrPort]struct{}) ([]netip.AddrPort, []netip.AddrPort, error) {
	l, err := netlinklistlistens.ListListens()
	if err != nil {
		return nil, nil, err
	}

	old := *m
	new := make(map[netip.AddrPort]struct{})
	*m = new
	added := make([]netip.AddrPort, 0)
	removed := make([]netip.AddrPort, 0)

	for _, addr := range l {
		new[addr] = struct{}{}

		_, exists := old[addr]
		if !exists {
			added = append(added, addr)
			continue
		}
		delete(old, addr)
	}

	for addr := range old {
		removed = append(removed, addr)
	}

	return added, removed, nil
}

type listenEventKind string

const (
	ADDED   listenEventKind = listenEventKind("added")
	REMOVED listenEventKind = listenEventKind("removed")
)

type listenEvent struct {
	kind listenEventKind
	addr netip.AddrPort
}

func watchListens(cx context.Context, callback func(listenEvent)) error {
	m := make(map[netip.AddrPort]struct{})

	d := time.Second * 1
	ticker := time.NewTicker(d)
	defer ticker.Stop()

	for {
		added, removed, err := updateListens(&m)
		if err != nil {
			return err
		}

		for _, addr := range added {
			callback(listenEvent{
				kind: ADDED,
				addr: addr,
			})
		}

		for _, addr := range removed {
			callback(listenEvent{
				kind: REMOVED,
				addr: addr,
			})
		}

		select {
		case <-cx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func exchange(cx context.Context, src net.Conn, dst *net.TCPConn) error {
	wg := sync.WaitGroup{}

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer dst.CloseWrite()

		_, _ = io.Copy(dst, src)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		defer dst.CloseRead()

		_, _ = io.Copy(src, dst)
	}()

	done := make(chan struct{})
	go func() {
		defer close(done)
		wg.Wait()
	}()

	select {
	case <-done:
	case <-cx.Done():
		src.Close()
		dst.Close()
		wg.Wait()
	}

	return nil
}

func forward(cx context.Context, client *ssh.Client, port uint16) error {
	log.Printf("Begin forward: %d\n", port)

	raddr := net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: int(port)}

	l, err := client.ListenTCP(&net.TCPAddr{IP: net.IPv4zero, Port: int(port)})
	if err != nil {
		return err
	}
	defer l.Close()

	c := make(chan struct {
		conn net.Conn
		err  error
	})

	go func() {
		defer close(c)

		for {
			conn, err := l.Accept()
			if err != nil {
				c <- struct {
					conn net.Conn
					err  error
				}{nil, err}
				break
			}
			c <- struct {
				conn net.Conn
				err  error
			}{conn, nil}
		}
	}()

	cx2, cancel := context.WithCancel(cx)
	wg := sync.WaitGroup{}

L:
	for {
		select {
		case r, ok := <-c:
			if !ok {
				break L
			}
			if r.err != nil {
				cancel()
				return r.err
			}
			src := r.conn

			wg.Add(1)
			go func() {
				defer wg.Done()
				defer src.Close()

				dst, err := net.DialTCP("tcp", nil, &raddr)
				if err != nil {
					log.Println(err)
					return
				}
				defer dst.Close()

				if err := exchange(cx2, src, dst); err != nil {
					log.Println(err)
				}
			}()

		case <-cx2.Done():
			break L
		}
	}

	cancel()
	wg.Wait()

	log.Printf("Done forward: %d\n", port)
	return nil
}

func loop(cx context.Context, client *ssh.Client) error {
	cx2, cancelAll := context.WithCancel(cx)

	wg := sync.WaitGroup{}
	forwards := make(map[uint16]context.CancelFunc)

	handler := func(event listenEvent) {
		log.Printf("%s %s\n", event.kind, event.addr)

		port := event.addr.Port()
		switch event.kind {
		case ADDED:
			{
				_, exists := forwards[port]
				if exists {
					log.Printf("Duplicate port %d\n", port)
					return
				}

				cx, cancel := context.WithCancel(cx2)
				forwards[port] = cancel

				wg.Add(1)
				go func() {
					defer wg.Done()

					if err := forward(cx, client, port); err != nil {
						log.Println(err)
					}
				}()
			}
		case REMOVED:
			{
				cancel, exists := forwards[port]
				if !exists {
					return
				}

				delete(forwards, port)
				cancel()
			}
		}
	}

	if err := watchListens(cx, handler); err != nil {
		cancelAll()
		wg.Wait()
		return err
	}

	cancelAll()
	wg.Wait()
	return nil
}

func run(cx context.Context, env *env) error {
	publickey, privatekey, err := sshKeygen()
	if err != nil {
		log.Fatal(err)
	}

	hostkey, err := hostkey(env.hostkeyPath)
	if err != nil {
		return err
	}

	publickeyPath, err := storePublickey(env.authorizedKeysDir, publickey)
	if err != nil {
		return err
	}
	defer os.Remove(publickeyPath)

	cfg := ssh.ClientConfig{
		User:            "user", // TODO configure
		HostKeyCallback: hostkey,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(privatekey),
		},
	}

	log.Printf("Dial server via %s...\n", env.sockPath)
	client, err := ssh.Dial("unix", env.sockPath, &cfg)
	if err != nil {
		return err
	}
	defer client.Close()

	log.Println("Forward is ready.")
	return loop(cx, client)
}

func exists(p string) (bool, error) {
	_, err := os.Stat(p)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func existsAll(ps ...string) (bool, error) {
	for _, p := range ps {
		e, err := exists(p)
		if err != nil {
			return false, err
		}
		if !e {
			return false, nil
		}
	}
	return true, nil
}

func waitServerReady(cx context.Context, datadir string) (*env, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	defer watcher.Close()

	serverPath := filepath.Join(datadir, "server")
	hostkeyPath := filepath.Join(serverPath, "rsa_hostkey.pub")
	sockPath := filepath.Join(serverPath, "ssh.sock")
	// TODO Need to check if the directory is valid

	if err := watcher.Add(serverPath); err != nil {
		return nil, err
	}

	for {
		ok, err := existsAll(hostkeyPath, sockPath)
		if err != nil {
			return nil, err
		}
		if ok {
			return &env{
				sockPath:          sockPath,
				hostkeyPath:       hostkeyPath,
				authorizedKeysDir: filepath.Join(datadir, "client"),
			}, nil
		}

		log.Println("Wait for the server side to start...")

		select {
		case <-watcher.Events:
		case err := <-watcher.Errors:
			return nil, err
		case <-cx.Done():
			return nil, errors.New("canceled.")
		}
	}
}

func main() {
	var datadir string
	flag.StringVar(&datadir, "datadir", "/run/devcontainer-portforward",
		"Directory where the volume shared with the server is mounted.")
	flag.Parse()

	cx, cancel := context.WithCancel(context.Background())
	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	defer close(c)

	go func() {
		defer cancel()
		<-c
	}()

	log.Println("Starting devcontainer-portforward client.")

	env, err := waitServerReady(cx, datadir)
	if err != nil {
		log.Fatal(err)
	}

	if err := run(cx, env); err != nil {
		log.Fatal(err)
	}
}
