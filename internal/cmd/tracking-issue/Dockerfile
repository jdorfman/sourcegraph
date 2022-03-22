FROM golang:1.17-alpine@sha256:c024b1fdb190e6537851cc2f9a83341980658d392941c0c8b7bda52ef3cdf079 AS builder

WORKDIR /go/src/tracking-issue
COPY . .
RUN go mod init tracking-issue
RUN go get ./...
RUN CGO_ENABLED=0 go install .

FROM sourcegraph/alpine-3.12:137550_2022-03-17_32d45d6a2a7f@sha256:d67684c174c577e7d61b4d7ef9d173fb73973f5b941bd65401dad90fc5e74353
COPY --from=builder /go/bin/* /usr/local/bin/
ENTRYPOINT ["tracking-issue"]
