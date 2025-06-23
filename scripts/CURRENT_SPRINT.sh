#!/bin/bash

go run cmd/sprint_tracker/main.go -sprint-filter="Platform 2025: Q2-4" | column -s, -t

