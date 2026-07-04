FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /queue .

FROM gcr.io/distroless/static-debian12
COPY --from=build /queue /queue
EXPOSE 8083
ENTRYPOINT ["/queue"]
