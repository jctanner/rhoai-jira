#!/bin/bash

go run cmd/sprint_lister/main.go -sprint-filter="Platform 2025: Q2-4" | column -s, -t

