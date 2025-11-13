#!/bin/bash

RC=0
# exclude createdAt from the diff
if [ $(git diff -I createdAt |wc -l ) -gt 0 ]; then
    echo "Unstaged or untracked changes exist:"
    git status
    git diff -I createdAt
    RC=1
else
    echo "git tree is clean"
fi

exit ${RC}
