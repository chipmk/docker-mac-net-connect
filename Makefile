PROJECT         := github.com/chipmk/docker-mac-net-connect
VERSION         := dev

build::
	go build -ldflags "-X github.com/chipmk/docker-mac-net-connect/version.Version=${VERSION}" ${PROJECT}