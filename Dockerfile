# We need a go compiler that's based on an image with libsystemd-dev installed,
FROM ubuntu:18.04 AS builder

RUN apt-get update && \
    apt-get install -y apt-transport-https ca-certificates build-essential git curl libsystemd-dev bzr

ENV GOROOT=/usr/local/go GOPATH=/go PATH=$PATH:/go/bin:/usr/local/go/bin

RUN curl -s -L https://storage.googleapis.com/golang/go1.8.3.linux-amd64.tar.gz > /tmp/go.tar.gz && \
    cd /usr/local && \
    tar -xzf /tmp/go.tar.gz && \
    rm -f /tmp/go.tar.gz && \
    mkdir /go && \
    go get github.com/kardianos/govendor

# Copy the ecs-logs sources so they can be built within the container.
COPY . /go/src/github.com/kapralVV/ecs-logs

# Build ecs-logs, then cleanup all unneeded packages.
RUN cd /go/src/github.com/kapralVV/ecs-logs && \
    govendor sync && \
    go build -o /usr/local/bin/ecs-logs

FROM ubuntu:18.04
RUN apt-get update && \
    apt-get install -y ca-certificates && \
    apt-get install -y iproute2 &&\
    apt-get clean -y

COPY --from=builder /usr/local/bin/ecs-logs /usr/local/bin/ecs-logs

# Sets the container's entry point.
ENTRYPOINT ["ecs-logs"]
