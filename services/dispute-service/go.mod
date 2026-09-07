module github.com/nexora/nexora/services/dispute-service

go 1.25

require (
	github.com/gocql/gocql v1.6.0
	github.com/google/uuid v1.6.0
	github.com/gorilla/mux v1.8.1
	github.com/nexora/nexora/shared v0.0.0
	github.com/rs/zerolog v1.35.1
)

require (
	github.com/golang-jwt/jwt/v5 v5.2.1 // indirect
	github.com/golang/snappy v0.0.4 // indirect
	github.com/hailocab/go-hostpool v0.0.0-20160125115350-e80d13ce29ed // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	golang.org/x/crypto v0.21.0 // indirect
	golang.org/x/sys v0.29.0 // indirect
	gopkg.in/inf.v0 v0.9.1 // indirect
)

replace github.com/nexora/nexora/shared => ../../shared
