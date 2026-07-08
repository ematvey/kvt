FROM golang:1.25-alpine AS build

RUN apk add --no-cache ca-certificates git
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o /out/kvt ./cmd/kvt

FROM alpine:3.20

RUN apk add --no-cache ca-certificates git openssh-client
COPY --from=build /out/kvt /usr/local/bin/kvt
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/docker-entrypoint.sh

EXPOSE 8200
ENTRYPOINT ["docker-entrypoint.sh"]
CMD ["serve", "--vault", "/workspace"]
