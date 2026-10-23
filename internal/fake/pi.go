package fake

import (
	"fmt"
	"os"
	"path/filepath"
)

// piEnv are the variables tkt agent sets for pi, which Pi records.
var piEnv = []string{"PI_CODING_AGENT_DIR", "PI_SKIP_VERSION_CHECK", "PI_TELEMETRY", "TKT_BIN", "TKT_VERSION", "TKT_PI_VERSION"}

// Pi stands in for the pi coding agent. It answers --version; any other
// call appends the variables tkt agent sets to $FAKE_DIR/pi-env, one
// KEY=VALUE line each, and exits.
func Pi() {
	logCall("pi")
	if len(os.Args) == 2 && os.Args[1] == "--version" {
		fmt.Println("1.3.0")
		os.Exit(0)
	}
	f, err := os.OpenFile(filepath.Join(os.Getenv("FAKE_DIR"), "pi-env"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		die(1, "fake pi: %v", err)
	}
	defer f.Close()
	for _, k := range piEnv {
		fmt.Fprintf(f, "%s=%s\n", k, os.Getenv(k))
	}
}
