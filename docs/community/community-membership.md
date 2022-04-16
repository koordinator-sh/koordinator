# Community membership

This doc outlines the various responsibilities of contributor roles in Koordinator.

| Role | Responsibilities | Requirements | Defined by |
| -----| ---------------- | ------------ | -------|
| Member | Active contributor in the community | Sponsored by 2 reviewers and multiple contributions to the project | Koordinator GitHub org member|
| Reviewer | Review contributions from other members | History of review and authorship in a subproject | `OWNERS` file reviewer entry |
| Approver | Contributions acceptance approval| Highly experienced active reviewer and contributor to a subproject | `OWNERS` file approver entry|

## New contributors

[New contributors](../../CONTRIBUTING.md) should be welcomed to the community by existing members, helped with PR
workflow, and directed to relevant documentation and communication channels.

## Established community members

Established community members are expected to demonstrate their adherence to the principles in this document,
familiarity with project organization, roles, policies, procedures, conventions, etc., and technical and/or writing
ability. Role-specific expectations, responsibilities, and requirements are enumerated below.

## Member

Members are continuously active contributors in the community. They can have issues and PRs assigned to them, and
pre-submit tests are automatically run for their PRs. Members are expected to remain active contributors to the
community.

**Defined by:** Member of the Koordinator GitHub organization

### Requirements

- Enabled [two-factor authentication](https://help.github.com/articles/about-two-factor-authentication) on their GitHub
  account
- Have made multiple contributions to the project or community. Contribution may include, but is not limited to:
    - Authoring or reviewing PRs on GitHub. At least one PR must be **merged**.
    - Filing or commenting on issues on GitHub
    - Contributing to community discussions (e.g. meetings, Slack, email discussion forums, Stack Overflow)
- Have read the [contributor guide](../../CONTRIBUTING.md)
- Sponsored by 2 reviewers. **Note the following requirements for sponsors**:
    - Sponsors must have close interactions with the prospective member - e.g. code/design/proposal review, coordinating
      on issues, etc.
    - Sponsors must be reviewers or approvers in at least one OWNERS file within one of the Koordinator Projects.
    - Sponsors must be from multiple member companies to demonstrate integration across community.
- Follow the instructions in [Joining the Koordinator GitHub Org](../../CONTRIBUTING.md#joining-the-community)
- Have your sponsoring reviewers reply confirmation of sponsorship: `+1`
- Once your sponsors have responded, your request will be reviewed by the Koordinator GitHub Admin team. Any missing
  information will be requested.

### Responsibilities and privileges

- Responsive to issues and PRs assigned to them
- Active owner of code they have contributed (unless ownership is explicitly transferred)
    - Code is well tested
    - Tests consistently pass
    - Addresses bugs or issues discovered after code is accepted
- Members can do `/lgtm` on open PRs.
- They can be assigned to issues and PRs, and people can ask members for reviews with a `/cc @username`.
- Tests can be run against their PRs automatically. No `/ok-to-test` needed.
- Members can do `/ok-to-test` for PRs that have a `needs-ok-to-test` label, and use commands like `/close` to close PRs
  as well.

## Reviewer

Reviewers are able to review code for quality and correctness on some part of a subproject. They are knowledgeable about
both the codebase and software engineering principles.

**Defined by:** *reviewers* entry in an OWNERS file in a repo owned by the Koordinator project.

### Requirements

The following apply to the part of codebase for which one would be a reviewer in an `OWNERS` file.

- member for at least 3 months
- Primary reviewer for at least 5 PRs to the codebase
- Reviewed or merged at least 20 substantial PRs to the codebase
- Knowledgeable about the codebase
- Sponsored by a approver
    - With no objections from other approvers
    - Done through PR to update the OWNERS file
- May either self-nominate, be nominated by an approver.

### Responsibilities and privileges

The following apply to the part of codebase for which one would be a reviewer in an `OWNERS` file.

- Tests are automatically run for PullRequests from members of the Koordinator GitHub organization
- Code reviewer status may be a precondition to accepting large code contributions
- Responsible for project quality control via code reviews
    - Focus on code quality and correctness, including testing and factoring
    - May also review for more holistic issues, but not a requirement
- Expected to be responsive to review requests as per community expectations
- Granted "read access" to koordinator repo

## Approver

Code approvers are able to both review and approve code contributions. While code review is focused on code quality and
correctness, approval is focused on holistic acceptance of a contribution including: backwards / forwards compatibility,
adhering to API and flag conventions, subtle performance and correctness issues, interactions with other parts of the
system, etc.

**Defined by:** *approvers* entry in an OWNERS file in a repo owned by the Koordinator project.

### Requirements

The following apply to the part of codebase for which one would be an approver in an `OWNERS` file.

- Reviewer of the codebase for at least 3 months
- Primary reviewer for at least 10 substantial PRs to the codebase
- Reviewed or merged at least 30 PRs to the codebase
- Nominated by a subproject owner
    - With no objections from other owners
    - Done through PR to update the top-level OWNERS file

### Responsibilities and privileges

The following apply to the part of codebase for which one would be an approver in an `OWNERS` file.

- Approver status may be a precondition to accepting large code contributions
- Demonstrate sound technical judgement
- Responsible for project quality control via code reviews
    - Focus on holistic acceptance of contribution such as dependencies with other features, backwards / forwards
      compatibility, API and flag definitions, etc
- Expected to be responsive to review requests as per community expectations
- Mentor contributors and reviewers
- May approve code contributions for acceptance

## Owners files

Most of these instructions are a modified version of
Kubernetes' [owners](https://github.com/kubernetes/community/blob/master/contributors/guide/owners.md)
guides.

### Overview of OWNERS files

OWNERS files are used to designate responsibility over different parts of the Kubeflow codebase. Today, we use them to
assign the **reviewer** and **approver** roles used in our two-phase code review process. Our OWNERS files were inspired
by [Chromium OWNERS files](https://chromium.googlesource.com/chromium/src/+/master/docs/code_reviews.md), which in turn
inspired [GitHub's CODEOWNERS files](https://help.github.com/articles/about-codeowners/).

The velocity of a project that uses code review is limited by the number of people capable of reviewing code. The
quality of a person's code review is limited by their familiarity with the code under review. Our goal is to address
both of these concerns through the prudent use and maintenance of OWNERS files

### OWNERS

Each directory that contains a unit of independent code or content may also contain an OWNERS file. This file applies to
everything within the directory, including the OWNERS file itself, sibling files, and child directories.

OWNERS files are in YAML format and support the following keys:

- `approvers`: a list of GitHub usernames or aliases that can `/approve` a PR
- `labels`: a list of GitHub labels to automatically apply to a PR
- `options`: a map of options for how to interpret this OWNERS file, currently only one:
    - `no_parent_owners`: defaults to `false` if not present; if `true`, exclude parent OWNERS files. Allows the use
      case where `a/deep/nested/OWNERS` file prevents `a/OWNERS` file from having any effect
      on `a/deep/nested/bit/of/code`
- `reviewers`: a list of GitHub usernames or aliases that are good candidates to `/lgtm` a PR

All users are expected to be assignable. In GitHub terms, this means they are either collaborators of the repo, or
members of the organization to which the repo belongs.

A typical OWNERS file looks like:

```
approvers:
  - alice
  - bob     # this is a comment
reviewers:
  - alice
  - carol   # this is another comment
  - sig-foo # this is an alias
```

#### OWNERS_ALIASES

Each repo may contain at its root an OWNERS_ALIAS file.

OWNERS_ALIAS files are in YAML format and support the following keys:

- `aliases`: a mapping of alias name to a list of GitHub usernames

We use aliases for groups instead of GitHub Teams, because changes to GitHub Teams are not publicly auditable.

A sample OWNERS_ALIASES file looks like:

```
aliases:
  sig-foo:
    - david
    - erin
  sig-bar:
    - bob
    - frank
```

GitHub usernames and aliases listed in OWNERS files are case-insensitive.