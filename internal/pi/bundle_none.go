//go:build !bundled_pi || !((linux || darwin) && (amd64 || arm64))

package pi

// bundle is the pi tarball for this system. Only a build with the
// bundled_pi tag, after go run ./internal/pi/fetch, embeds one.
var bundle []byte
