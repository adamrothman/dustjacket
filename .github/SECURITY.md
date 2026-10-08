# Security

Please report a vulnerability privately: on this repository's
**Security and quality** tab, choose **Report a vulnerability**. Don't
open a public issue or pull request for it.

This covers the code here and its one deployment,
https://dustjacket.rothman.tools. There are no releases or supported
versions: what's on `main` is what's deployed. A problem in Hardcover
itself is for Hardcover.

The deployment admits only the Hardcover accounts on its allowlist. To
try something out, run the server locally instead:
`go run ./cmd/dustjacket serve -memory` needs no AWS.
