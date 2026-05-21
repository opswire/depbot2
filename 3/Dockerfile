FROM docker.io/library/nginx:1.25.3@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa AS builder
FROM ghcr.io/myorg/myapp:2.1.0
# Следующие два FROM пропускаются: нет домена или нон-semver тег
FROM alpine:latest
FROM nginx:1.2.3
RUN echo hello
