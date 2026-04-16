Feature: Structured sync API
  As a Formidable client pulling content over HTTP
  I want to probe the current HEAD of a repository
  So that I can detect whether there is new content to sync

  Scenario: HEAD on missing repo returns 404
    Given the server is running
    When I GET "/api/repos/ghost-repo/head"
    Then the response status should be 404

  Scenario: HEAD on empty repo returns 409
    Given the server is running
    And I POST "/api/repos" with body '{"name":"empty-sync"}'
    When I GET "/api/repos/empty-sync/head"
    Then the response status should be 409

  Scenario: HEAD on scaffolded repo returns version and default_branch
    Given the server is running
    And I POST "/api/repos" with body '{"name":"sync-scaffold","scaffold_formidable":true}'
    When I GET "/api/repos/sync-scaffold/head"
    Then the response status should be 200
    And the response body should contain "\"default_branch\":\"master\""
    And the response body should contain "\"version\":\""

  Scenario: Tree on missing repo returns 404
    Given the server is running
    When I GET "/api/repos/ghost-repo/tree"
    Then the response status should be 404

  Scenario: Tree on empty repo returns 409
    Given the server is running
    And I POST "/api/repos" with body '{"name":"empty-tree"}'
    When I GET "/api/repos/empty-tree/tree"
    Then the response status should be 409

  Scenario: Tree on scaffolded repo lists the seeded files
    Given the server is running
    And I POST "/api/repos" with body '{"name":"tree-scaffold","scaffold_formidable":true}'
    When I GET "/api/repos/tree-scaffold/tree"
    Then the response status should be 200
    And the response body should contain "templates/basic.yaml"
    And the response body should contain ".formidable/context.json"
    And the response body should contain "storage/.gitkeep"

  Scenario: Tree with unresolvable version returns 422
    Given the server is running
    And I POST "/api/repos" with body '{"name":"tree-bad","scaffold_formidable":true}'
    When I GET "/api/repos/tree-bad/tree?version=deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
    Then the response status should be 422

  Scenario: Snapshot on missing repo returns 404
    Given the server is running
    When I GET "/api/repos/ghost-repo/snapshot"
    Then the response status should be 404

  Scenario: Snapshot on empty repo returns 409
    Given the server is running
    And I POST "/api/repos" with body '{"name":"empty-snap"}'
    When I GET "/api/repos/empty-snap/snapshot"
    Then the response status should be 409

  Scenario: Snapshot on scaffolded repo returns files with base64 content
    Given the server is running
    And I POST "/api/repos" with body '{"name":"snap-scaffold","scaffold_formidable":true}'
    When I GET "/api/repos/snap-scaffold/snapshot"
    Then the response status should be 200
    And the response body should contain "\"path\":\"templates/basic.yaml\""
    And the response body should contain "\"content_b64\":\""

  Scenario: Snapshot with unresolvable version returns 422
    Given the server is running
    And I POST "/api/repos" with body '{"name":"snap-bad","scaffold_formidable":true}'
    When I GET "/api/repos/snap-bad/snapshot?version=deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
    Then the response status should be 422

  Scenario: File read on missing repo returns 404
    Given the server is running
    When I GET "/api/repos/ghost-repo/files/README.md"
    Then the response status should be 404

  Scenario: File read on empty repo returns 409
    Given the server is running
    And I POST "/api/repos" with body '{"name":"empty-file"}'
    When I GET "/api/repos/empty-file/files/README.md"
    Then the response status should be 409

  Scenario: File read on scaffolded repo returns base64 content
    Given the server is running
    And I POST "/api/repos" with body '{"name":"file-scaffold","scaffold_formidable":true}'
    When I GET "/api/repos/file-scaffold/files/templates/basic.yaml"
    Then the response status should be 200
    And the response body should contain "\"path\":\"templates/basic.yaml\""
    And the response body should contain "\"content_b64\":\""

  Scenario: File read of a path that does not exist returns 404
    Given the server is running
    And I POST "/api/repos" with body '{"name":"file-missing","scaffold_formidable":true}'
    When I GET "/api/repos/file-missing/files/does/not/exist.txt"
    Then the response status should be 404

  Scenario: File read with unresolvable version returns 422
    Given the server is running
    And I POST "/api/repos" with body '{"name":"file-badver","scaffold_formidable":true}'
    When I GET "/api/repos/file-badver/files/templates/basic.yaml?version=deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
    Then the response status should be 422
