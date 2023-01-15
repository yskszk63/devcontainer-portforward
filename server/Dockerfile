FROM debian:bullseye-slim

RUN export DEBIAN_FRONTEND=noninteractive &&\
    apt update &&\
    apt install --no-install-recommends -y openssh-server socat &&\
    apt clean &&\
    rm -rf /var/lib/apt/lists/* &&\
    rm /etc/ssh/ssh_host_*_key* &&\
    mkdir -p /data/server /data/client /app/hostkey &&\
    useradd -s /usr/sbin/nologin user &&\
    chown user /data/server /app/hostkey

VOLUME /data
USER user

COPY ./sshd_config ./authorized_keys.sh ./run.sh /app/

CMD ["/app/run.sh"]
