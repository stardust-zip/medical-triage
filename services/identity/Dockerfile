FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /identity .

FROM gcr.io/distroless/static-debian12
COPY --from=build /identity /identity
EXPOSE 8082
ENTRYPOINT ["/identity"]
