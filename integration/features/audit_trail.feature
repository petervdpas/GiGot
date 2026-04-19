Feature: Tamper-evident audit trail on refs/audit/main
  Every repo GiGot serves carries a server-authored audit chain at
  refs/audit/main — one commit per audited operation, each pointing at the
  previous entry by hash. Formidable clients can git-fetch it to see what
  happened to the repo without a new API surface, and the chain travels
  with any mirror.

  Scenario: Creating a repo writes one audit entry of type repo_create
    Given the server is running
    When I POST "/api/repos" with body '{"name":"audit-create"}'
    Then the response status should be 201
    And the audit ref in repo "audit-create" has 1 entries
    And the top audit event in repo "audit-create" has type "repo_create"

  Scenario: A successful PUT /files advances the audit chain
    Given the server is running in formidable-first mode
    When I POST "/api/repos" with body '{"name":"audit-put"}'
    Then the response status should be 201
    And the audit ref in repo "audit-put" has 1 entries
    When I GET "/api/repos/audit-put/head"
    And I save the JSON response "version" as "head0"
    And I put binary file "templates/new.yaml" in repo "audit-put" with bytes "6e616d653a206e6577" and parent "${head0}"
    Then the response status should be 200
    And the audit ref in repo "audit-put" has 2 entries
    And the top audit event in repo "audit-put" has type "file_put"

  Scenario: A successful POST /commits advances the audit chain
    Given the server is running in formidable-first mode
    When I POST "/api/repos" with body '{"name":"audit-commit"}'
    Then the response status should be 201
    And the audit ref in repo "audit-commit" has 1 entries
    When I GET "/api/repos/audit-commit/head"
    And I save the JSON response "version" as "head0"
    And I POST "/api/repos/audit-commit/commits" with body '{"parent_version":"${head0}","changes":[{"op":"put","path":"templates/a.yaml","content_b64":"YTogMQo="},{"op":"put","path":"templates/b.yaml","content_b64":"Yjogewo="}],"message":"two files"}'
    Then the response status should be 200
    And the audit ref in repo "audit-commit" has 2 entries
    And the top audit event in repo "audit-commit" has type "commit"
