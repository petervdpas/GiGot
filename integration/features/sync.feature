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

  Scenario: File write on missing repo returns 404
    Given the server is running
    When I PUT "/api/repos/ghost-repo/files/a.txt" with body '{"parent_version":"HEAD","content_b64":"eA=="}'
    Then the response status should be 404

  Scenario: File write on empty repo returns 409
    Given the server is running
    And I POST "/api/repos" with body '{"name":"put-empty"}'
    When I PUT "/api/repos/put-empty/files/a.txt" with body '{"parent_version":"HEAD","content_b64":"eA=="}'
    Then the response status should be 409

  Scenario: File write missing parent_version returns 400
    Given the server is running
    And I POST "/api/repos" with body '{"name":"put-noparent","scaffold_formidable":true}'
    When I PUT "/api/repos/put-noparent/files/a.txt" with body '{"content_b64":"eA=="}'
    Then the response status should be 400

  Scenario: File write with bad base64 returns 400
    Given the server is running
    And I POST "/api/repos" with body '{"name":"put-badb64","scaffold_formidable":true}'
    And I GET "/api/repos/put-badb64/head"
    And I save the JSON response "version" as "head"
    When I PUT "/api/repos/put-badb64/files/a.txt" with body '{"parent_version":"${head}","content_b64":"not base64!!"}'
    Then the response status should be 400

  Scenario: File write with unresolvable parent_version returns 422
    Given the server is running
    And I POST "/api/repos" with body '{"name":"put-badparent","scaffold_formidable":true}'
    When I PUT "/api/repos/put-badparent/files/a.txt" with body '{"parent_version":"deadbeefdeadbeefdeadbeefdeadbeefdeadbeef","content_b64":"eA=="}'
    Then the response status should be 422

  Scenario: File write fast-forwards when parent equals HEAD
    Given the server is running
    And I POST "/api/repos" with body '{"name":"put-ff","scaffold_formidable":true}'
    And I GET "/api/repos/put-ff/head"
    And I save the JSON response "version" as "head"
    When I PUT "/api/repos/put-ff/files/templates/basic.yaml" with body '{"parent_version":"${head}","content_b64":"bmV3Cg=="}'
    Then the response status should be 200
    And the response body should contain "\"version\":\""

  Scenario: File write returns changes[] with the touched path and new blob
    Given the server is running
    And I POST "/api/repos" with body '{"name":"put-ch","scaffold_formidable":true}'
    And I GET "/api/repos/put-ch/head"
    And I save the JSON response "version" as "head"
    When I PUT "/api/repos/put-ch/files/templates/basic.yaml" with body '{"parent_version":"${head}","content_b64":"bmV3Cg=="}'
    Then the response status should be 200
    And the response body should contain "\"changes\":["
    And the response body should contain "\"path\":\"templates/basic.yaml\""
    And the response body should contain "\"op\":\"modified\""
    And the response body should contain "\"blob\":\""

  Scenario: File write auto-merges when parent is a strict ancestor of HEAD
    Given the server is running
    And I POST "/api/repos" with body '{"name":"put-am","scaffold_formidable":true}'
    And I GET "/api/repos/put-am/head"
    And I save the JSON response "version" as "head0"
    And I PUT "/api/repos/put-am/files/other.txt" with body '{"parent_version":"${head0}","content_b64":"c2VydmVyCg=="}'
    When I PUT "/api/repos/put-am/files/templates/basic.yaml" with body '{"parent_version":"${head0}","content_b64":"Y2xpZW50Cg=="}'
    Then the response status should be 200
    And the response body should contain "\"merged_from\":\""
    And the response body should contain "\"merged_with\":\""

  Scenario: File write with a conflicting stale parent returns 409 with blob triple
    Given the server is running
    And I POST "/api/repos" with body '{"name":"put-cf","scaffold_formidable":true}'
    And I GET "/api/repos/put-cf/head"
    And I save the JSON response "version" as "head0"
    And I PUT "/api/repos/put-cf/files/templates/basic.yaml" with body '{"parent_version":"${head0}","content_b64":"c2VydmVyLWVkaXQK"}'
    When I PUT "/api/repos/put-cf/files/templates/basic.yaml" with body '{"parent_version":"${head0}","content_b64":"Y2xpZW50LWVkaXQK"}'
    Then the response status should be 409
    And the response body should contain "\"path\":\"templates/basic.yaml\""
    And the response body should contain "\"base_b64\":\""
    And the response body should contain "\"theirs_b64\":\""
    And the response body should contain "\"yours_b64\":\""

  Scenario: Multi-file commit on missing repo returns 404
    Given the server is running
    When I POST "/api/repos/ghost-repo/commits" with body '{"parent_version":"HEAD","changes":[{"op":"put","path":"a","content_b64":"eA=="}]}'
    Then the response status should be 404

  Scenario: Multi-file commit on empty repo returns 409
    Given the server is running
    And I POST "/api/repos" with body '{"name":"c-empty"}'
    When I POST "/api/repos/c-empty/commits" with body '{"parent_version":"HEAD","changes":[{"op":"put","path":"a","content_b64":"eA=="}]}'
    Then the response status should be 409

  Scenario: Multi-file commit with missing parent_version returns 400
    Given the server is running
    And I POST "/api/repos" with body '{"name":"c-noparent","scaffold_formidable":true}'
    When I POST "/api/repos/c-noparent/commits" with body '{"changes":[{"op":"put","path":"a","content_b64":"eA=="}]}'
    Then the response status should be 400

  Scenario: Multi-file commit with empty changes returns 400
    Given the server is running
    And I POST "/api/repos" with body '{"name":"c-noch","scaffold_formidable":true}'
    And I GET "/api/repos/c-noch/head"
    And I save the JSON response "version" as "head"
    When I POST "/api/repos/c-noch/commits" with body '{"parent_version":"${head}","changes":[]}'
    Then the response status should be 400

  Scenario: Multi-file commit with bad op returns 400
    Given the server is running
    And I POST "/api/repos" with body '{"name":"c-badop","scaffold_formidable":true}'
    And I GET "/api/repos/c-badop/head"
    And I save the JSON response "version" as "head"
    When I POST "/api/repos/c-badop/commits" with body '{"parent_version":"${head}","changes":[{"op":"nuke","path":"a"}]}'
    Then the response status should be 400

  Scenario: Multi-file commit with unresolvable parent_version returns 422
    Given the server is running
    And I POST "/api/repos" with body '{"name":"c-badver","scaffold_formidable":true}'
    When I POST "/api/repos/c-badver/commits" with body '{"parent_version":"deadbeefdeadbeefdeadbeefdeadbeefdeadbeef","changes":[{"op":"put","path":"a","content_b64":"eA=="}]}'
    Then the response status should be 422

  Scenario: Multi-file commit returns changes[] listing every touched path with its op
    Given the server is running
    And I POST "/api/repos" with body '{"name":"c-changes","scaffold_formidable":true}'
    And I GET "/api/repos/c-changes/head"
    And I save the JSON response "version" as "head"
    When I POST "/api/repos/c-changes/commits" with body '{"parent_version":"${head}","message":"mixed ops","changes":[{"op":"delete","path":"templates/basic.yaml"},{"op":"put","path":"templates/new.yaml","content_b64":"bmV3Cg=="}]}'
    Then the response status should be 200
    And the response body should contain "\"changes\":["
    And the response body should contain "\"path\":\"templates/basic.yaml\""
    And the response body should contain "\"op\":\"deleted\""
    And the response body should contain "\"path\":\"templates/new.yaml\""
    And the response body should contain "\"op\":\"added\""

  Scenario: Multi-file rename produces exactly one commit
    Given the server is running
    And I POST "/api/repos" with body '{"name":"c-rn","scaffold_formidable":true}'
    And I GET "/api/repos/c-rn/head"
    And I save the JSON response "version" as "head"
    When I POST "/api/repos/c-rn/commits" with body '{"parent_version":"${head}","message":"rename basic","changes":[{"op":"delete","path":"templates/basic.yaml"},{"op":"put","path":"templates/renamed.yaml","content_b64":"eA=="}]}'
    Then the response status should be 200
    And the response body should contain "\"version\":\""

  Scenario: Multi-file commit aborts transactionally on conflict
    Given the server is running
    And I POST "/api/repos" with body '{"name":"c-cf","scaffold_formidable":true}'
    And I GET "/api/repos/c-cf/head"
    And I save the JSON response "version" as "head0"
    And I PUT "/api/repos/c-cf/files/templates/basic.yaml" with body '{"parent_version":"${head0}","content_b64":"c2VydmVyLWVkaXQK"}'
    When I POST "/api/repos/c-cf/commits" with body '{"parent_version":"${head0}","changes":[{"op":"put","path":"templates/basic.yaml","content_b64":"Y2xpZW50Cg=="},{"op":"put","path":"new.txt","content_b64":"bmV3Cg=="}]}'
    Then the response status should be 409
    And the response body should contain "\"conflicts\":["
    And the response body should contain "\"path\":\"templates/basic.yaml\""

  Scenario: Multi-file commit method not allowed
    Given the server is running
    And I POST "/api/repos" with body '{"name":"c-meth","scaffold_formidable":true}'
    When I GET "/api/repos/c-meth/commits"
    Then the response status should be 405

  Scenario: Changes on missing repo returns 404
    Given the server is running
    When I GET "/api/repos/ghost-repo/changes?since=HEAD"
    Then the response status should be 404

  Scenario: Changes on empty repo returns 409
    Given the server is running
    And I POST "/api/repos" with body '{"name":"ch-empty"}'
    When I GET "/api/repos/ch-empty/changes?since=HEAD"
    Then the response status should be 409

  Scenario: Changes without since returns 400
    Given the server is running
    And I POST "/api/repos" with body '{"name":"ch-nosince","scaffold_formidable":true}'
    When I GET "/api/repos/ch-nosince/changes"
    Then the response status should be 400

  Scenario: Changes with unresolvable since returns 422
    Given the server is running
    And I POST "/api/repos" with body '{"name":"ch-badsince","scaffold_formidable":true}'
    When I GET "/api/repos/ch-badsince/changes?since=deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"
    Then the response status should be 422

  Scenario: Changes with since equal to HEAD returns empty diff
    Given the server is running
    And I POST "/api/repos" with body '{"name":"ch-noop","scaffold_formidable":true}'
    And I GET "/api/repos/ch-noop/head"
    And I save the JSON response "version" as "head"
    When I GET "/api/repos/ch-noop/changes?since=${head}"
    Then the response status should be 200
    And the response body should contain "\"changes\":[]"

  Scenario: Changes lists paths added between since and HEAD
    Given the server is running
    And I POST "/api/repos" with body '{"name":"ch-added","scaffold_formidable":true}'
    And I GET "/api/repos/ch-added/head"
    And I save the JSON response "version" as "head0"
    And I PUT "/api/repos/ch-added/files/templates/new.yaml" with body '{"parent_version":"${head0}","content_b64":"bmV3Cg=="}'
    When I GET "/api/repos/ch-added/changes?since=${head0}"
    Then the response status should be 200
    And the response body should contain "\"path\":\"templates/new.yaml\""
    And the response body should contain "\"op\":\"added\""

  Scenario: Changes reports added, modified, and deleted in one response
    Given the server is running
    And I POST "/api/repos" with body '{"name":"ch-mix","scaffold_formidable":true}'
    And I GET "/api/repos/ch-mix/head"
    And I save the JSON response "version" as "head0"
    And I POST "/api/repos/ch-mix/commits" with body '{"parent_version":"${head0}","message":"mix a/m/d","changes":[{"op":"delete","path":"templates/basic.yaml"},{"op":"put","path":"README.md","content_b64":"dXBkYXRlZAo="},{"op":"put","path":"templates/new.yaml","content_b64":"bmV3Cg=="}]}'
    When I GET "/api/repos/ch-mix/changes?since=${head0}"
    Then the response status should be 200
    And the response body should contain "\"path\":\"templates/basic.yaml\""
    And the response body should contain "\"op\":\"deleted\""
    And the response body should contain "\"path\":\"README.md\""
    And the response body should contain "\"op\":\"modified\""
    And the response body should contain "\"path\":\"templates/new.yaml\""
    And the response body should contain "\"op\":\"added\""

  Scenario: Changes method not allowed
    Given the server is running
    And I POST "/api/repos" with body '{"name":"ch-meth","scaffold_formidable":true}'
    When I POST "/api/repos/ch-meth/changes?since=HEAD" with body '{}'
    Then the response status should be 405
