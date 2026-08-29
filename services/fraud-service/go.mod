module github.com/nexora/nexora/services/fraud-service

go 1.24

require (
	github.com/IBM/sarama v1.43.0
	github.com/gocql/gocql v1.6.0
	github.com/google/uuid v1.6.0
	github.com/gorilla/mux v1.8.1
	github.com/rs/zerolog v1.32.0
	github.com/nexora/nexora/shared v0.0.0
)

replace github.com/nexora/nexora/shared => ../../shared
