#!/bin/sh
# Renders the app icons and the link preview image into public/ with a
# headless Chromium-based browser. Run from client/ after editing art/:
#   art/render.sh
# Set BROWSER to another Chrome, Chromium, Edge or Brave binary if needed.
set -eu
cd "$(dirname "$0")"

BROWSER=${BROWSER:-}
if [ -z "$BROWSER" ]; then
	for b in \
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" \
		"/Applications/Brave Browser.app/Contents/MacOS/Brave Browser" \
		"/Applications/Chromium.app/Contents/MacOS/Chromium" \
		"$(command -v chromium 2>/dev/null || true)" \
		"$(command -v google-chrome 2>/dev/null || true)"; do
		if [ -n "$b" ] && [ -x "$b" ]; then BROWSER=$b; break; fi
	done
fi
[ -n "$BROWSER" ] || { echo "no Chromium-based browser found; set BROWSER" >&2; exit 1; }

shot() { # source width height output
	"$BROWSER" --headless --disable-gpu --hide-scrollbars --force-device-scale-factor=1 \
		--virtual-time-budget=5000 --window-size="$2,$3" --screenshot="$4" "file://$PWD/$1" 2>/dev/null
}

out=../public
mkdir -p "$out/icons"
shot icon.svg 512 512 "$out/icons/icon-512.png"
shot icon-maskable.svg 512 512 "$out/icons/icon-maskable-512.png"
shot og-image.html 1200 630 "$out/og-image.png"

# Smaller sizes from the 512px render. sips ships with macOS; elsewhere use
# ImageMagick.
resize() { # input size output
	if command -v sips >/dev/null; then sips -z "$2" "$2" "$1" --out "$3" >/dev/null
	else magick "$1" -resize "$2x$2" "$3"; fi
}
resize "$out/icons/icon-512.png" 192 "$out/icons/icon-192.png"
resize "$out/icons/icon-512.png" 180 "$out/icons/apple-touch-icon.png"
echo "rendered icons and og-image.png into public/"
