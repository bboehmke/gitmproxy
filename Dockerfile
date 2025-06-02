FROM golang:1.24 as builder

COPY . /src/
WORKDIR /src/

RUN CGO_ENABLED=0 go build -ldflags "-s -w" -o /gitmproxy .

FROM scratch

# copy app from build image
COPY --from=builder /gitmproxy /gitmproxy
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

EXPOSE 8090
VOLUME ["/data"]
WORKDIR "/data"

ENTRYPOINT ["/gitmproxy"]
