# What is a Good Koordinator PR

You have made some changes to Koordinator and you want to submit to the Koordinator team for inclusion in the main
release.

How does this work? In summary, we will look at the following:

- [Presence of unit tests](#presence-of-unit-tests)
- [Adherence to the coding style](#adherence-to-the-coding-style)
- [Adequate in-line comments](#adequate-in-line-comments)
- [Explanatory commit message and release note annotation](#explanatory-commit-message-and-release-note-annotation)
- [Separating changes into multiple commits](#separating-changes-into-multiple-commits)

Is this your first time submitting a PR? Check [Your first Koordinator PR](./first-pr.md)!

## Presence of unit tests

Every Koordinator change that fixes a bug or changes functionality **must be accompanied by testing code.**

A unit test is good when:

1. it would fail if ran on Koordinator before your change,
2. it succeeds when ran after the change,
3. it does not test too many other things besides the area you're changing,
4. it is fully automated.

If your change affects multiple areas, there should probably be a different unit test for each area.

Currently, in the Koordinator project we use the following types of test:

- Go unit tests, via functions named `Test_XXX` placed in the same Go package as the change. This is the most common.
    - Use this when a change is local to a few functions and these functions can be exercised in isolation.
    - Take inspiration from the other tests in the same package.
- Other tests are coming soon...

## Adherence to the coding style

During a review, some attention is given to the [coding style](TODO).

1. Koordinator uses [golangci-lint](https://golangci-lint.run/) to find bugs and performance issues, 
   offer simplifications, and enforce style rules. We advise to integrate this in your text editor.
2. We value good naming for variables, functions for new concepts, etc. If your change extends an area where there is
   already an established naming pattern, you can reuse the existing pattern. If you are introducing new concepts, be
   creative! But during the review we might kindly request you to rename some entities.

## Adequate in-line comments

We value in-line comments that clarify what is going on in the Go code.

1. Good comments:
    - are written in grammatical English, with full sentences (capital at beginning, period at end).
    - do not repeat what the code does (i.e. do not write `// This loop iterates from 1 to 10 in front of a for-loop`),
      but instead clarifies the goal and/or the motivation (
      e.g. `// This loop aggregates a result over all the instances`).
2. We value adequate (but not excessive) single empty lines to separate blocks of code that correspond to units of
   reasoning.
3. In some cases we even value adding comments on code that you have not changed, but that was unclear to you (when the
   comments would add a future reader).

## Explanatory commit message and release note annotation

Do not confuse the Git commit message and the PR description message!

As much as possible, **put all the information about your change primarily in the git commit message**, and merely
maintain the PR description as a copy or a summary of the git commit message.

- GitHub shows the PR description message, however, when developing, we get context and navigate code using the Git
  commit message.
- The PR description message is auto-populated by GitHub from the Git commit message, so it "magically" works if the
  information is there in the first place.
- If the discussion on a PR enables a new understanding, be careful to **update the git commit message** accordingly.
- Place your [release note(s)](TODO) in git commit messages.

See the separate page on [Git Commit Messages](TODO).

## Separating changes into multiple commits

Short, simple projects produce a PR containing a single, small commit. However, it is often the case that a PR grows to
contain multiple commits.

This happens mainly in two situations:

- A larger project may be decomposed in sub-parts / steps. **We recommend one commit per logical change in the PR, up to
  and including when the PR is merged.**
- During code review, to answer reviewer comments. **We accept seeing fix-up commits during the review, but we request
  they are merged together (“squashed”) before the PR is merged.**
- Generally, we do not wish to review PRs that contain more than a dozen commits. A large number of commits is a symptom
  that the change is disorganized and will put undue burden on the reviewer team. If you think you need to make a large
  change, please approach our team on Slack or DingTalk first to discuss how to organize the work.

See [Decomposing changes and updating PRs during reviews](TODO) for details.

