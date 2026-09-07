FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY shared/ ./shared/
COPY services/fraud-service/ ./services/fraud-service/
RUN cd services/fraud-service && CGO_ENABLED=0 GOOS=linux go build -mod=mod -ldflags="-s -w" -o /app/server ./cmd/main.go

FROM alpine:3.19
RUN apk --no-cache add ca-certificates tzdata wget
RUN addgroup -S nexora && adduser -S nexora -G nexora
COPY --from=builder /app/server /server
EXPOSE 8089
HEALTHCHECK --interval=15s --timeout=5s --retries=5 --start-period=20s CMD wget --spider -q http://localhost:8089/health || exit 1
USER nexora
CMD ["/server"]
