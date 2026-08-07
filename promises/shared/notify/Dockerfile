FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -o notify .

FROM alpine:3.19
RUN apk --no-cache add ca-certificates && \
    addgroup -g 65532 nonroot && \
    adduser -D -u 65532 -G nonroot nonroot
WORKDIR /app
COPY --from=builder /app/notify .
USER 65532:65532
ENTRYPOINT ["/app/notify"]