FROM docker.m.daocloud.io/golang:alpine AS builder

RUN apk add --no-cache --update git build-base
ENV CGO_ENABLED=0

WORKDIR /app

COPY . /app

WORKDIR /app/src
ARG BUILD_VERSION=0.0.0-dev
RUN go mod download
RUN go build -trimpath -ldflags="-s -w -X github.com/guyuxiang/scplus-pay/config.BuildVersion=${BUILD_VERSION}" -o /app/scplus-pay .

FROM docker.m.daocloud.io/alpine:latest AS runner
ENV TZ=Asia/Shanghai
RUN apk --no-cache add ca-certificates tzdata
ARG API_RATE_URL=""

WORKDIR /app
COPY --from=builder /app/src/.env.example /app/.env
RUN if [ -n "$API_RATE_URL" ]; then \
      sed -i "s|^api_rate_url=.*$|api_rate_url=${API_RATE_URL}|" /app/.env; \
    fi
COPY --from=builder /app/scplus-pay .

VOLUME /app/conf
ENTRYPOINT ["./scplus-pay", "http", "start"]
