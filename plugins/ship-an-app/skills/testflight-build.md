---
description: Get a build to testers: version, build number, notes and who to send it to.
applies: work on an app release or a beta
---

# When to use
A work item that needs a build in testers' hands (TestFlight or a Play testing track).

# How
1. Check the release the work belongs to (list_releases) and use its version. Bump the build number; never reuse one.
2. Build from the release branch, not a feature branch.
3. Write "What to test" in two to four lines: what changed and where to look. Plain words.
4. Say which group gets it (internal first, then external once internal has tried it).
5. Post the build number and the testing link on the work item with add_note.

# Check before handing over
- The version and build number match the release in Enplace.
- Anything that needs a sign-in has a test account in the notes.
