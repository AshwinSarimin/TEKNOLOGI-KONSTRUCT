FROM golang:1.21-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY pipeline.go .

RUN CGO_ENABLED=0 GOOS=linux go build -o pipeline pipeline.go

FROM alpine:latest

RUN apk --no-cache add ca-certificates && \
    addgroup -g 65532 nonroot && \
    adduser -D -u 65532 -G nonroot nonroot

WORKDIR /app

COPY --from=builder /app/pipeline .

USER 65532:65532

CMD ["./pipeline"]
