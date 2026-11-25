# Supplemental upstream license texts

`notices.py` collects license files from the Go toolchain and the runtime
dependencies of every release target. Some generated Unicode data files instead
point to upstream terms without a separate local license text. Keep that text
here so package builds need no additional license download.

## Unicode-3.0.txt

- Original source: <https://www.unicode.org/license.txt>
- Retrieved: 2026-11-25; Unicode License V3, copyright 1991–2026 Unicode, Inc.
- SHA-256: `e7a93b009565cfce55919a381437ac4db883e9da2126fa28b91d12732bc53d96`
- Relevant generated data: `github.com/rivo/uniseg` v0.4.7
  (`graphemeproperties.go`, Unicode 15.0.0) and
  `github.com/clipperhouse/uax29/v2` v2.7.0 (`graphemes/trie.go`, Unicode 17.0.0).

The file is copied verbatim from Unicode. Its
[licensing policy](https://www.unicode.org/policies/licensing_policy.html)
describes coverage for data and software. Preserve the upstream notices shipped
with the modules as well; this supplement does not replace them.

When changing dependencies, review embedded and generated data notices that
automatic filename discovery cannot find. Record any additional source and
reason here, and include its original text through `notices.py`.
