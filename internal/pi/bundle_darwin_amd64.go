//go:build bundled_pi

package pi

import _ "embed"

//go:embed bundle/pi-darwin-x64.tar.gz
var bundle []byte
