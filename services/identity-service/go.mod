module github.com/nexora/nexora/services/identity-service

go 1.24

require (
	github.com/IBM/sarama v1.43.0
	github.com/gocql/gocql v1.6.0
	github.com/golang-jwt/jwt/v5 v5.2.1
	github.com/google/uuid v1.6.0
	github.com/gorilla/mux v1.8.1
	github.com/rs/zerolog v1.32.0
	github.com/nexora/nexora/shared v0.0.0
	golang.org/x/crypto v0.21.0
)

replace github.com/nexora/nexora/shared => ../../shared
