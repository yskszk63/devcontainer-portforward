#!/bin/sh

# TODO requirements., portability

set -e

OS=`uname -s`
ARCH=`uname -m`
VERSION="0.0.1-beta4"
URL="https://github.com/yskszk63/devcontainer-portforward/releases/download/v${VERSION}/devcontainer-portforward_${VERSION}_${OS}_${ARCH}.tar.gz"

mkdir -p /run/devcontainer-portforward
mkdir /run/devcontainer-portforward/client
mkdir /run/devcontainer-portforward/server
chown 1000 /run/devcontainer-portforward/server

tmp=`mktemp -d`
trap "rm -rf ${tmp}" EXIT
curl -sSfL "${URL}" -o "${tmp}/devcontainer-portforward.tar.gz"
tar xf "${tmp}/devcontainer-portforward.tar.gz" -C "${tmp}"
mv "${tmp}/devcontainer-portforward" /usr/local/bin

cp ./entrypoint.sh /usr/local/bin/devcontainer-portforward-entrypoint.sh
