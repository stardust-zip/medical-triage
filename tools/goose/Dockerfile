FROM golang:1.25-alpine AS build
RUN go install github.com/pressly/goose/v3/cmd/goose@latest

FROM alpine:3.22
COPY --from=build /go/bin/goose /usr/local/bin/goose
WORKDIR /migrations
ENTRYPOINT ["goose"]
