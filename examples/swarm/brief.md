# How does gilda keep a long session within the model's context window?

Answer for a maintainer deciding whether to add a summarising `/compact` command. Cite files
and line numbers. The repository is the working directory.

## Elision of old tool results

When are old tool results replaced by stubs, which results are kept, and how does the model
learn it can call the tool again?

## The full-window refusal

depends: Elision of old tool results

What happens near the end of the window: the 85% warning, the 95% refusal, and how a
provider's own context error is told apart from other errors.

## Background tasks and their context

How do `task` subagents keep searches and file contents out of the calling conversation, and
what of a task's work reaches its caller?
