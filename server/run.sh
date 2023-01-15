#!/bin/sh

ssh-keygen -q -f /app/hostkey/rsa_hostkey -N '' -t rsa
cp /app/hostkey/*.pub /data/server/

sshd_cmd='/usr/sbin/sshd -f /app/sshd_config -De'
if [ -n "$SSHD_GATEAY_PORTS" ]; then
    sshd_cmd="$sshd_cmd -oGatewayPorts=$SSHD_GATEAY_PORTS"
fi

if [ -n "$SSHD_LISTEN_PORT" ]; then
    exec $sshd_cmd -p$SSHD_LISTEN_PORT
fi

exec socat -ddd UNIX-LISTEN:/data/server/ssh.sock,reuseaddr,fork "EXEC:$sshd_cmd -i"
