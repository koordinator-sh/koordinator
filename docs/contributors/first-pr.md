# Your first Koordinator PR

This document is a long-winded guide to preparing and submitting your first contribution to Koordinator. It's primarily
intended as required reading for new Koordinator engineers, but may prove useful to external contributors too.

## The development cycle

At the time of this writing, Koordinator is several hundred thousand lines of Go code. This section is a whirlwind tour
of what lives where.

### First things first

Since Koordinator is open source, most of the instructions for hacking on Koordinator live in this
repo, [koordinator-sh/koordinator](https://github.com/koordinator-sh/koordinator). Then, look at
our [Go (Golang) coding guidelines](TODO) and
the [Google Go Code Review](https://github.com/golang/go/wiki/CodeReviewComments) guide it links to. If you haven't
written any Go before Koordinator, you may want to hold off on reviewing the style guide until you've written your first
few functions in go.

### Start coding!

It's time to fire up your editor and get to work! `Makefile` do a good job of describing the basic development commands
you'll need. In short, you'll use `make` to drive builds.

To produce all Koordinator related binaries:

```
$ make build
```

To run all the tests:

```
$ make test
```

### A note on adding a new file

You'll notice that all source files in this repository have a license notification at the top. Be sure to copy this
license into any files that you create.

We also include a convenient command for you to generate all missing license:

```
$ make lint-license
```

## When to submit

Code at Koordinator is not about perfection. It's too easy to misinterpret "build it right" as "build it perfectly," but
striving for perfection can lead to over-engineered code that took longer than necessary to write. Especially as a
startup that moves quickly, building it right often means building the simplest workable solution, then iterating as
necessary. What we try to avoid at Koordinator is the quick, dirty, gross hack - but even that can be acceptable to plug
an urgent leak.

So, you should get feedback early and often, while being cognizant of your reviewer's time. You don't want to ask for a
detailed review on a rough draft, but you don't want to spend a week heading down the wrong path, either.

You have a few options for choosing when to submit:

1. You can open a PR as a draft. Github bot will automatically assign the `do-not-merge/work-in-progress` label to your
   PR. This is useful when you're not entirely sure who should review your code.
2. Especially on your first PR, you can do everything short of opening a GitHub PR by sending someone a link to your
   local branch. Alternatively, open a PR against your fork. This won't send an email notification to anyone watching
   this repository, this works well when you have a reviewer in mind.
3. For small changes where the approach seems obvious, you can open a PR with what you believe to be production-ready or
   near-production-ready code. As you get more experience with how we develop code, you'll find that larger and larger
   changes will begin falling into this category.

PRs are assumed to be production-ready unless you say otherwise in the PR description. Be sure to note if your PR is
a `WIP` to save your reviewer time.

If you find yourself with a PR (`git diff --stat`) that exceeds roughly 500 line of code, it's time to consider
splitting the PR in two, or at least introducing multiple commits. This is impossible for some PRs—especially
refactors—but "the feature is only partially complete" should never be a reason to balloon a PR.

One common approach used here is to build features up incrementally, even if that means exposing a partially-complete
feature in a release. For example, suppose you were tasked with implementing support for a new QoS feature, like
supporting Network QoS in Koordlet. You might reasonably submit 4 separate PRs. First, you could land a change that
introduce new API fields in `NodeSLO` like `bandwidthThreshold`, `intervalSeconds`, and etc. Then, you might land a
change to implement a naive version of the feature, with tests to verify its correctness. A third PR might introduce
some performance optimizations, and a fourth PR some refactors that you didn't think of until a few weeks later. This
approach is totally encouraged! It's no problem if the first PR lands in an unstable release, as long as a change isn't
backing you into a corner or causing server panics. Code produced from an incremental approach is much easier to review,
which means better, more robust code.

### Production-ready code

In general, production-ready code:

- Is free of syntax errors and typos
- Omits accidental code movement, stray whitespace, and stray debugging statements
- Documents all exported structs and functions
- Documents internal functions that are not immediately obvious
- Has a well-considered design

That said, don't be embarrassed when your review points out syntax errors, stray whitespace, typos, and missing
docstrings! That's why we have reviews. These properties are meant to guide you in your final scan.

## How to submit

So, you've written your code and written your tests. It's time to send your code for review.

### Fix your style violations

First, read the [Go (Golang) coding guidelines]() again, looking for any style violations. It's easier to remember a
style rule once you've violated it. Then, run our suite of linters:

```
$ make lint
```

### Where to push

In the interest of branch tidiness, we ask that even contributors with the commit bit avoid pushing directly to this
repository. That deserves to be properly called out:
> **Pro-tip**: don't push to koordinator-sh/koordinator directly!

Instead, create yourself a fork on Github. This will give you a semi-private personal workspace
at `github.com/YOUR-USERNAME/koordinator`. Code you push to your personal fork is assumed to be a work-in-progress(WIP),
dangerously broken, and otherwise unfit for consumption.

Then, you'll need to settle on a name for your branch, if you haven't already. This isn't a hard-and-fast rule, but
Koordinator convention is to hyphenate-your-feature. These branch identifiers serve primarily to identify to you.

To give you a sense, here's a few feature branches that had been merged in recently when this document was written, and
their associated commit summaries:

- shinytang6:fix/koordlet: `feat(koordlet): support memoryEvictLowerPercent in memory evict`
- jasonliu747:cache: `chore: remove additional cache in golangci-lint`

It's far more important to give the commit message a descriptive title and message than getting the branch name right.

### Split your change into logical chunks

Be kind to other developers, including your future self, by splitting large commits. Each commit should represent one
logical change to the codebase with a descriptive message that explains the rationale for the change.

On your first PR, it's worth re-reading the [Git Commit Messages](TODO) page to ensure your commit messages follow the
guidelines.

Most importantly, all commits which touch this repository should have the format `pkg: message`, where `pkg` is the name
of the package that was most affected by the change. A few engineers still "watch" this repository, which means they
receive emails about every issue and PR. Having the affected package at the front of every commit message makes it
easier for these brave souls to filter out irrelevant emails.

### Who to request a review from

If you're unable to get a reviewer recommendation, the `Author` listed at the top of the file may be your best bet. You
should check that the hardcoded author agrees with the Git history of the file, as the hardcoded author often gets
stale. Use `git log FILE` to see all commits that have touched a particular file or directory sorted by most recent
first, or use `git blame FILE` to see the last commit that touched a particular line. The GitHub web UI or a Git plugin
for your text editor of choice can also get the job done. If there's a clear winner, ask them for a review; if they're
not the right reviewer, they'll suggest someone else who is. In cases that are a bit less clear, you may want to note in
a comment on GitHub that you're not confident they're the right reviewer and ask if they can suggest someone better.

It's important to have a tough reviewer. I've seen a few PRs land that absolutely **_shouldn't_** have landed, each time
because the person who understood why the PR was a bad idea wasn't looped into the review. There's no shame in this — it
happens! I say this merely as a reminder that a tough review is a net positive, as it can save a lot of pain down the
road.

Also, note that GitHub allows for both "assignees" and "reviewers" on a pull request. It's not entirely clear what the
distinction between these fields is. Choose the field you like most and move on. Ever since GitHub began auto-suggesting
reviewers, there seems to be a slight preference for using the reviewer field instead of the assignee field.

### Write your PR description

**Nearly everything you're tempted to put in a PR description belongs in a [Git commit message](TODO)**. (As an
extension, nearly everything you put in a commit message belongs in code comments too.)

You should absolutely write a PR description manually if you're looking for a non-complete review—i.e., if your code is
a WIP.

Otherwise, something simple like "see commit messages for details" is good enough. On PRs with just one commit, GitHub
will automatically include that one commit's message in the description; it's totally fine to leave that in the PR
description.

## The code review

Code reviews are one of the main ways for Koordinator to guarantee quality in the product. We use them to ensure that
all code written meets our strict standards for production-ready code.

A code review is like a discussion. The reviewer can have questions, and can even sometimes request the author to write
a deeper justification for changes. The goal of that discussion is to end up with the highest quality product, and
create a track record of why and how a change occurred, so we can later look back and understand the choices.

No one is expected to write perfect code on the first try. That's why we have code reviews in the first place!

### Addressing feedback

If someone leaves line comments on your PR without leaving a top-level "looks good to me" (LGTM), it means they feel you
should address their line comments before merging. That is, without an LGTM, all comments are roughly in the discussing
disposition. If you agree with their feedback, you should make the requested change; if you disagree, you must leave a
comment with your counterargument and wait to hear back.

If someone leaves a top-level LGTM/accepts your PR using Github but also leaves line comments, you can assume all line
comments are in the satisfied disposition. If you disagree with any of their feedback, you should still let them know (
e.g., "actually, I think my way is better because reasons"), but you don't need to wait to hear back from them to merge.

Once you've accumulated a set of changes you need to make to address review feedback, it's time to force-push a new
revision. Be sure to amend your prior commits—you don't want to tack on a bunch of fixup commits to address review
feedback. If you've split your PR into multiple commits, it can be tricky to ensure your review feedback changes make it
into the right commit. A tutorial on how to do so is outside the scope of this document, but the magic keywords to
Google for are `git interactive rebase`.

You should respond to all unresolved comments whenever you push a new revision, or before you merge, even if it's just
to say "Done."

> **Pro-tip**: Wait to publish any "Done" comments until you've verified that your changes have been force-pushed and show up properly in Reviewable. It's very easy to preemptively draft a "Done" comment as you scroll through feedback, but then forget to actually make the change.

### Merging

To merge code into any Koordinator repository, you need at least one LGTM (or Github PR approval). Some LGTMs matter
more than others; you want to make sure you solicit an LGTM from whoever "owns" the code you're touching.

Occasionally, someone will jump into a review with some minor style nits and leave an LGTM once they see you've
addressed the nits; you should still wait for the primary reviewer you selected to LGTM the merits of the design. To
confuse matters, sometimes you'll get a drive-by review from someone who was as qualified or more qualified than the
primary reviewer. In that case, their LGTM is merge-worthy. Usually it's clear which situation you're dealing with, but
if in doubt, ask!

When your PR is ready to go, approver would leave an `approved` label to your PR. Once approved, your PR will be
automatically merged by our robot after all CIs have passed. This limits your exposure by ensuring you don't
accidentally merge a commit with an obviously broken build or merge skew.

Congratulations on landing your first PR! It only gets easier from here.