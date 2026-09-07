module github.com/nexora/nexora/services/control-plane-service

go 1.25

toolchain go1.25.0

require (
	github.com/google/uuid v1.6.0
	github.com/gorilla/mux v1.8.1
	github.com/nexora/nexora/shared v0.0.0
	github.com/rs/zerolog v1.35.1
)

require (
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	golang.org/x/sys v0.29.0 // indirect
)

replace github.com/nexora/nexora/shared => ../../shared
