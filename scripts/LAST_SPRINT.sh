#!/bin/bash

 ~/go/bin/go1.24.3 run cmd/sprint_tracker/main.go -project=RHOAIENG -sprint-filter="Platform 2025: Q2-3" | column -s, -t

