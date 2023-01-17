#!/bin/sh

set -e

/usr/local/bin/devcontainer-portforward & < /dev/null 2>&1 > /dev/null
exec "$@"
