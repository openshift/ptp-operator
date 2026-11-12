# Tracking
## Bugs
Bugs should all be tracked through github issues in the ptp-operator repo.  Issues should not be raised in the linuxptp-daemon repo, except where for repo-specific things (e.g. issues with image builds in the repo).  This is in order to allow all project bugs to be tracked together
## Features
Features should be tracked through github issues in the ptp-operator repo.

# Submissions
## Submission expectations:
Contributors are responsible for ensuring the following with their submissions:
- Contributors are responsible for only adding code that they wrote, or is licensed appropriately.  No GPL code should be imported.
- Code is tested adequately
    - See https://github.com/k8snetworkplumbingwg/ptp-operator/blob/master/TESTING.md for more details on expectations of testing prior to submission
- Code introduces sufficient new test coverage
    - See https://github.com/k8snetworkplumbingwg/ptp-operator/blob/master/TESTING.md for more details on expectations of added test coverage
- We expect that each tracked issue (feature or bug) will be a single complete submission per repo(daemon and operator), rather than several partial/incomplete submissions, unless not possible for technical reasons
    - all tracking under ptp-operator repo
- Commit messages will adequately describe what’s being done
    - Title: summarize the code change
    - Title: include the bug ticket #, but not a copy of bug title (bug title can be included in the description)
    - Description: explain the Root Cause and Solution
- PR is linked to a github issue under the ptp-operator repo
- PR will follow best practices, be well written, and adequately commented.
## Commit expectations
A PR may contain several commits. Each commit should be easy to review, revert and bisect on its own:
- Keep commits small, self-consistent and atomic: one logical change per commit, together with the tests and generated or vendored files that change needs.
- Refactoring needed for a feature or fix may be submitted in the same PR, but it must be in its own commit, separate from the feature or fix.
- Every commit must pass CI on its own, including lint and formatting checks, unit tests and the other required checks. It is not enough for only the final commit of the PR to pass.
- Each commit message must explain both what the commit changes and why the change is needed (for example the root cause or motivation), not only what was done.
## Review expectations
Maintainers are responsible for ensuring that adequate review is conducted.  This includes the following:
- PR is aligned with the design plan of the project
- Issues should be rejected if going against maintainer design intent
- PR adequately addresses the issue it’s intended to fix
- PR code follows best practices
- PR does not break anything
- PR does not introduce any security issues
- PR does not introduce any potential performance issues
Testing by submitter is adequate
- PR adds necessary test coverage
- PR commit message is descriptive of what’s being done
- PR is not introducing proprietary code, except in plugin section
