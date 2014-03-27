#!/bin/bash

while [ 1 ]
do
   clear
   echo "building mesh..."
   go build mesh.go
   echo ""
   echo "building meshTest..."
   go build meshTest.go
   echo "done."
   sleep 4
done
