#!/bin/bash

DIR=$(readlink -f $(dirname $0))

[ $$ -eq 1 ] && sleep 10

py.test -s --tb=short \
          --verbose --junitxml=$DIR/results.xml \
          $DIR/tests/test_*.py "$@"
