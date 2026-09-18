# Stage 1: build the Go orchestrator binary
FROM golang:1.21-alpine AS go-builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -o pipeline .

# Stage 2: extract the Terraform binary
FROM hashicorp/terraform:1.7 AS terraform-source

# Stage 3: minimal runtime image
FROM alpine:3.19
RUN apk --no-cache add ca-certificates && \
    addgroup -g 65532 nonroot && \
    adduser -D -u 65532 -G nonroot nonroot

# Terraform binary
COPY --from=terraform-source /bin/terraform /usr/local/bin/terraform

# Go orchestrator
COPY --from=go-builder /app/pipeline /app/pipeline

# Terraform module (baked into the image — wraps the platform storage account
# pattern). chown so the non-root user can write .terraform/ + lock file at
# runtime; HOME must also be writable for terraform's own plugin cache.
COPY --chown=nonroot:nonroot terraform/ /app/terraform/
ENV HOME=/tmp

USER 65532:65532
ENTRYPOINT ["/app/pipeline"]