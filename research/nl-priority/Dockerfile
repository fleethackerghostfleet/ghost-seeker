# Research only. Product Dockerfiles and their input allowlists stay unchanged.
FROM golang:1.26.10-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY edge ./edge
COPY shore ./shore
COPY internal ./internal
COPY gen ./gen
COPY research/nlpriority ./research/nlpriority
COPY research/cmd/nl-priority ./research/cmd/nl-priority
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/nl-priority ./research/cmd/nl-priority
FROM alpine:3.21
RUN addgroup -g 10001 research && adduser -D -u 10001 -G research research && mkdir /data && chown research:research /data
COPY --from=build /out/nl-priority /usr/local/bin/nl-priority
COPY research/nl-priority/web /web
USER research
EXPOSE 8097
ENTRYPOINT ["/usr/local/bin/nl-priority"]
CMD ["--listen", ":8097", "--data", "/data", "--web-dir", "/web", "--llm-url", "http://granite:8080", "--vision-url", "http://vision:8098"]
