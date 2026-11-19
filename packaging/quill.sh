#!/bin/sh
d="${QUILL_LIBEXEC:-/usr/libexec/quill}"
if [ -n "$WAYLAND_DISPLAY" ] && [ "$QUILL_X11" != 1 ]; then
	exec "$d/quill-wayland" "$@"
fi
exec "$d/quill-x11" "$@"
