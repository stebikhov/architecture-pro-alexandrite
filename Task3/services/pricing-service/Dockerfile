FROM golang:1.21-alpine AS builder

WORKDIR /app

RUN apk add --no-cache git

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o /pricing-service .

FROM alpine:latest

WORKDIR /

COPY --from=builder /pricing-service /pricing-service

EXPOSE 8080

CMD ["/pricing-service"]
