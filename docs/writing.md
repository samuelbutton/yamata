# Writing guide

Use [ASD-STE100, Issue 9](https://www.asd-ste100.org/assets/files/ASD-STE100_ISSUE9.pdf) for new project documents.
The rules below apply that standard to this project.
Keep legal notices and exact command syntax unchanged.

## Sentences and procedures

Use active voice and give each sentence one topic.
Keep instructions within 20 words per sentence and descriptions within 25 words.
Limit each paragraph to one topic and six sentences.
Use the same term for the same concept.
Define necessary technical terms in the [glossary](glossary.md).

Give each procedure its prerequisites, commands, expected result, and cleanup steps.
State the directory from which the reader must run commands.
Explain failures and limits beside the relevant procedure.
Distinguish implemented behavior from future work.
Provide all necessary context within the public repository.

## Names and terms

Use Yamata as the project name.
Use generic terms for other systems, organizations, people, and examples.
Examples include simulation worker, analysis worker, queue, recording, and client.
Use public technology names only when they explain the implementation.
Preserve required repository coordinates, license notices, and dependency attribution.

Exclude former employer names, private system names, and their aliases.
This rule also applies to comparisons, acknowledgments, and statements about inspiration.
Do not publish private source notes or links to them.
Apply the rule to code, comments, paths, test data, output, diagrams, documents, and publication text.

## Review

Prerequisites: the changed files, the glossary, and the standard's rules and dictionary.

1. Read each changed sentence for its intended meaning.
2. Check its words and meanings against the dictionary.
3. Check necessary technical terms against the glossary.
4. Check sentence length, paragraph length, and active voice.
5. Review names, links, examples, and diagrams for private references.
6. Run each changed command example and compare the result with its description.
7. Run the cleanup steps from each example.

The expected result is accurate, self-contained documentation with repeatable examples.
Text searches and word counts support this review but cannot prove compliance.
