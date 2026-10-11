# Handlebars execution adapter

This directory contains the root Go package of aymerick/raymond v2.0.2,
under its original MIT license (see LICENSE). Its AST, lexer and parser remain
external dependencies at that exact version.

Breaking-IT modifications:
- Options-only helper callbacks receive arbitrary positional arguments through Options.
- Stringer implementations take precedence over underlying slices when rendering.
- Negative indexes cannot panic during field resolution.

This adapter allows request multivalue fields to render their first value while
retaining indexed access and allows WireMock helpers with optional arguments.
- HTML escaping is disabled, as in WireMock 3.13.2 response templates.
- Request header maps can resolve property names without case sensitivity.
- Ordered JSON objects preserve source field order during field lookup and iteration.
- Helpers can distinguish their own block from an enclosing block's subexpression.
