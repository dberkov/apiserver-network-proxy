# Build the proxy-server binary
FROM golang:1.12.1 as builder

# Copy in the go src
WORKDIR /go/src/sigs.k8s.io/apiserver-network-proxy
COPY pkg/    pkg/
COPY cmd/    cmd/
COPY proto/  proto/
COPY vendor/ vendor/

# Build
ARG ARCH
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${ARCH} go build -a -ldflags '-extldflags "-static"' -o proxy-server sigs.k8s.io/apiserver-network-proxy/cmd/proxy

# Copy the loader into a thin image
FROM scratch
WORKDIR /
COPY --from=builder /go/src/sigs.k8s.io/apiserver-network-proxy/proxy-server .
ENTRYPOINT ["/proxy-server"]
