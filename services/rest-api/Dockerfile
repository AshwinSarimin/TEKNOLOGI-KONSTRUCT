FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -o rest-api .

FROM alpine:3.19
RUN apk --no-cache add ca-certificates && \
    addgroup -g 65532 nonroot && \
    adduser -D -u 65532 -G nonroot nonroot
WORKDIR /app
COPY --from=builder /app/rest-api .
USER 65532:65532
ENTRYPOINT ["/app/rest-api"]
