#!/bin/sh
BODY='{"email":"abc@gmail.com","password":"12345678"}'
LEN=$(echo -n "$BODY" | wc -c)
{
printf "POST /v1/auth/login HTTP/1.1\r\nHost: localhost:8081\r\nContent-Type: application/json\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s" "$LEN" "$BODY"
} | nc -w 5 localhost 8081
