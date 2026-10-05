# WhatsApp REST API loopback binding

The merged call-recovery bridge constructs `:3456`, exposing its REST listener on all interfaces. Maya requires local-only services. Preserve every route, port, default mux and WhatsApp lifecycle; construct the existing HTTP server with `127.0.0.1:<port>` and use that server's ListenAndServe method. No environment override or alternate address is added.

A constructor permits a meaningful regression: production port3456 has the exact loopback address; port0 binds an actual temporary TCP listener whose local address is IPv4 loopback and serves the existing mux. Close the listener/server in cleanup. No WhatsApp login, live datastore, send, service restart or installed binary change during tests.

Reuse: existing net/http Server and default mux own listener behavior. This is a bounded exposure correction, not a new server framework. Independent security/test reviews precede implementation; run focused RED/GREEN, full Go test, race and vet. Merge the reviewed fork PR, then build from exact fork main with source/binary hashes. Activation remains separately controlled by the Maya release.

Verification: the constructor retaining the old wildcard failed the regression with `got ":3456"`; the loopback fix passes the full Go suite and race suite, and go vet reports no findings. Five independent prebuild lenses completed; remote compatibility and lifecycle findings were dispositioned because loopback-only is required and the existing goroutine/server lifetime is unchanged.
