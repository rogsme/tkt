//go:build bundled_pi

package pi

import _ "embed"

//go:embed bundle/pi-linux-x64.tar.gz
var bundle []byte
