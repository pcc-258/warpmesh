#!/bin/sh
set -e

Xvfb :1 -screen 0 1280x800x24 &
sleep 1

export DISPLAY=:1
fluxbox &
xsetroot -solid "#171d24" &

x11vnc -display :1 -forever -shared -rfbport 5900 -nopw -bg

exec /usr/local/bin/warpmesh-agent "$@"
