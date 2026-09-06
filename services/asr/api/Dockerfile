# asr-api: thin HTTP facade in front of the two vLLM services.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/asr-api .

FROM alpine:3.22
RUN apk add --no-cache ca-certificates wget \
    && adduser -D -u 10001 asrapi
COPY --from=build /out/asr-api /usr/local/bin/asr-api
USER asrapi
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/asr-api"]
