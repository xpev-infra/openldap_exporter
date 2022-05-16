FROM golang:alpine3.13 AS build_base

ARG HTTPS_PROXY
ARG HTTP_PROXY

RUN apk add --no-cache gcc g++ make bash git

ENV GO111MODULE=on
ENV GOPROXY=https://goproxy.cn
WORKDIR /src
COPY go.mod .
COPY go.sum .
RUN go mod download

FROM build_base AS binary_builder

ARG HTTPS_PROXY
ARG HTTP_PROXY

COPY . /src
WORKDIR /src
RUN make compile

FROM alpine:3.13

ARG HTTPS_PROXY
ARG HTTP_PROXY

RUN apk add tzdata --no-cache

COPY --from=binary_builder /src/target/openldap_exporter  /usr/local/bin/openldap_exporter