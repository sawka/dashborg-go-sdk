#!/bin/bash

set -x
protoc --go_out=plugins=grpc,paths=source_relative:. pkg/dashproto/dashproto.proto
