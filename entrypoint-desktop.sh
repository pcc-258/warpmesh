#!/bin/sh
set -e

# The container rootfs persists across restarts; remove stale Xvfb state before
# starting a new desktop session.
rm -f /tmp/.X1-lock /tmp/.X11-unix/X1

Xvfb :1 -screen 0 1440x900x24 -nolisten tcp &
xvfb_pid=$!
export DISPLAY=:1

i=0
while [ ! -S /tmp/.X11-unix/X1 ] && [ "$i" -lt 15 ]; do
  if ! kill -0 "$xvfb_pid" 2>/dev/null; then
    echo "Xvfb exited before the display socket became ready" >&2
    exit 1
  fi
  sleep 1
  i=$((i + 1))
done

if [ ! -S /tmp/.X11-unix/X1 ]; then
  echo "Timed out waiting for the X11 display socket" >&2
  exit 1
fi

# Start a complete XFCE session on the virtual display, including its panel,
# window manager, desktop, and session D-Bus.
dbus-run-session -- startxfce4 >/tmp/xfce-session.log 2>&1 &

x11vnc -display :1 -forever -shared -rfbport 5900 -nopw -bg

exec /usr/local/bin/warpmesh-agent "$@"
