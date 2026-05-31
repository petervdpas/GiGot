Feature: Structured per-field merge for Formidable records (Phase F1)
  As a Formidable client whose saves can race
  I want the server to merge records field-by-field
  So that two edits to different fields on the same record both land
  without me having to resolve a line-based JSON conflict by hand.

  # Covers §10.2–§10.3 + §11 F1. Every data field is atomic: disjoint
  # edits merge, but both sides changing the same field (or an immutable
  # meta field: created, id, template) is a conflict surfaced as 409, not
  # a silent last-writer-wins. Full parity with git: no edit silently lost.

  Scenario: Two clients editing disjoint data fields on the same record auto-merge
    Given the server is running in formidable-first mode
    When I POST "/api/repos" with body '{"name":"rec-merge"}'
    Then the response status should be 201
    And I GET "/api/repos/rec-merge/head"
    And I save the JSON response "version" as "head0"
    And I put a record "storage/addresses/oak.meta.json" in repo "rec-merge" with data '{"name":"Oak","country":"nl"}' updated "2025-01-01T00:00:00Z" and parent "${head0}"
    And the response status should be 200
    And I GET "/api/repos/rec-merge/head"
    And I save the JSON response "version" as "head1"
    And I put a record "storage/addresses/oak.meta.json" in repo "rec-merge" with data '{"name":"Oak","country":"uk"}' updated "2025-02-01T00:00:00Z" and parent "${head1}"
    And the response status should be 200
    When I put a record "storage/addresses/oak.meta.json" in repo "rec-merge" with data '{"name":"Oak Rd","country":"nl"}' updated "2025-03-01T00:00:00Z" and parent "${head1}"
    Then the response status should be 200
    And the resulting record "storage/addresses/oak.meta.json" in repo "rec-merge" has data field "name" equal to "Oak Rd"
    And the resulting record "storage/addresses/oak.meta.json" in repo "rec-merge" has data field "country" equal to "uk"

  Scenario: Client mutating immutable meta.created is refused with a conflict
    # Immutable-meta divergence surfaces as 409 carrying the per-field
    # conflict, so the client sees its write was refused instead of being
    # silently reconciled to HEAD. The whole write aborts; HEAD is unchanged.
    Given the server is running in formidable-first mode
    When I POST "/api/repos" with body '{"name":"rec-imm"}'
    Then the response status should be 201
    And I GET "/api/repos/rec-imm/head"
    And I save the JSON response "version" as "head0"
    And I put a record "storage/addresses/oak.meta.json" in repo "rec-imm" with data '{"name":"Oak"}' updated "2025-01-01T00:00:00Z" and parent "${head0}"
    And the response status should be 200
    And I GET "/api/repos/rec-imm/head"
    And I save the JSON response "version" as "head1"
    And I put a record "storage/addresses/oak.meta.json" in repo "rec-imm" with data '{"name":"Oak"}' updated "2025-02-01T00:00:00Z" and parent "${head1}"
    And the response status should be 200
    When I put a record "storage/addresses/oak.meta.json" in repo "rec-imm" with data '{"name":"Oak"}' created "1999-01-01T00:00:00Z" updated "2025-03-01T00:00:00Z" and parent "${head1}"
    Then the response status should be 409
    And the response body should contain "immutable"
    And the resulting record "storage/addresses/oak.meta.json" in repo "rec-imm" has data field "name" equal to "Oak"
