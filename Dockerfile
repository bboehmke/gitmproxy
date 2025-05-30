FROM golang:1.24

COPY . /src/
WORKDIR /src/

RUN CGO_ENABLED=0 go build -ldflags "-s -w" -o /gitmproxy .

FROM scratch

# copy app from build image
COPY --from=0 /gitmproxy /gitmproxy

EXPOSE 8090
VOLUME ["/data"]
WORKDIR "/data"

ENTRYPOINT "/gitmproxy"
