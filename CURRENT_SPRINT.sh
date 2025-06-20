#!/bin/bash

go run sprint_tracker.go -sprint-filter="Platform 2025: Q2-4" | column -s, -t

