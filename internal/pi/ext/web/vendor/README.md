# Vendored browser libraries

The web page loads these as they are, with no build step. Bun serves them
from the extension's directory.

| File | From | License |
|---|---|---|
| `preact.js` | `htm@3.1.1`, `preact/standalone.module.js`: htm with the Preact 10 and hooks it bundles | `htm.LICENSE` (Apache 2.0), `preact.LICENSE` (MIT) |
| `marked.js` | `marked@18.1.1`, `lib/marked.esm.js`, without its source map line | `marked.LICENSE` (MIT) |

To update one, download the package with `npm pack NAME@VERSION`, copy the
file over, drop its `//# sourceMappingURL=` line, and change the version here.
