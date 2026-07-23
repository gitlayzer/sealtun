FROM golang:1.25.12-alpine AS builder

WORKDIR /app
ENV GOPROXY=https://proxy.golang.org|direct
RUN apk add --no-cache ca-certificates git
COPY go.mod go.sum* ./
RUN go mod download

COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s -X github.com/labring/sealtun/pkg/version.Version=${VERSION}" -o sealtun .

FROM scratch

COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /app/sealtun /sealtun
ENTRYPOINT ["/sealtun"]
