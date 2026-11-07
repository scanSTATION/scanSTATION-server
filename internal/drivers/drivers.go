// Package drivers links the scanner drivers into a binary. Each driver is
// included only when its build tag is set, so the build decides which
// scanner libraries (submodules) and firmware an image contains:
//
//	go build -tags "v370 epsonscan2" ./cmd/...
//	docker build --build-arg DRIVERS="v370" .
//
// Adding a driver: a package internal/drivers/<name> that calls
// driver.Register in init(), a file <name>.go here with the build tag
// <name>, and its library as a submodule listing the driver in
// <lib>/scanstation/drivers.
package drivers
