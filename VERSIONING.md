# Operator Releases
Operator releases are linked to openshift releases.  Versioning will be consistent with versioning of Openshift releases and release builds.
Operator releases are branches cut from master downstream, and should reference upstream release tags, which should be merged in before cutting.  Once it’s cut from that upstream tagged releases, everything should be brought in only by cherrypick not merge.

# Upstream Releases
Upstream releases should be tagged whenever there are significant new features added, or updated versions of key dependencies.  Release notes should indicate these.
Upstream releases are tagged only.  Tags will be done simultaneously in both upstream daemon and operator repos.

# Upstream linkage
When downstream branches are cut, link to latest upstream release.  When backporting commits from upstream, there should be no changes to the upstream release the given downstream branch points to.  Similar to how RHEL releases point to specific kernel versions, regardless of backported commits.

# Issue linkage
Upon tagging an upstream release, all github issues (bugs or features) should be included in release notes.  Jira should be used for downstream tracking of these, referencing the downstream release it’s fixed in.