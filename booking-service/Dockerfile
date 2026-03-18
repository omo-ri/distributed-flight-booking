FROM golang:1.25-alpine AS builder

WORKDIR /src

# Copy flight-service (needed for local replace directive)
COPY flight-service /src/flight-service

# Copy booking-service
COPY booking-service /src/booking-service

WORKDIR /src/booking-service
RUN go mod download
RUN CGO_ENABLED=0 go build -o /booking-service ./cmd

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /booking-service /app/booking-service
COPY --from=builder /src/booking-service/migrations /app/migrations
ENTRYPOINT ["/app/booking-service"]
