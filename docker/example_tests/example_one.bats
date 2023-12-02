#!/usr/bin/env bats

setup() {
    MYVAR=1
}

@test "example_one: check that MYVAR is 1" {
	[ "$MYVAR" == 1 ] || false
}

@test "example_one: check that MYVAR is not 2" {
	[ "$MYVAR" != 2 ] || false
}

@test "example_one: something skipped" {
	skip
	[ "$MYVAR" != 2 ] || false
}

@test "example_one: something failing" {
	[ "$MYVAR" != 1 ] || false
}
