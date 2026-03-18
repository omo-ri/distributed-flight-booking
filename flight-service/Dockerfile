FROM golang:1.25-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /flight-service ./cmd

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /flight-service /app/flight-service
COPY --from=builder /app/migrations /app/migrations
ENTRYPOINT ["/app/flight-service"]
