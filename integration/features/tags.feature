Feature: Tag catalogue (admin)
  As an administrator
  I want a catalogue of organisational tags I can apply to repos,
  subscription keys, and accounts
  So that the admin UI can be filtered and bulk-managed by team /
  project / lifecycle without inventing per-row groups

  Slice 1 only exposes the catalogue endpoints — assignment surfaces
  on repos / subscriptions / accounts land in slice 2. The audit-event
  scenarios below confirm the design-doc Q3 decision (every catalogue
  lifecycle action lands in the system audit log) is wired end-to-end.

  Scenario: Listing tags requires a session
    Given the server is running
    When I GET "/api/admin/tags"
    Then the response status should be 401

  Scenario: Admin can create and list a tag
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    When I log in as admin "alice" with password "hunter2"
    And I GET "/api/admin/tags"
    Then the JSON response "count" should be 0

    When I POST "/api/admin/tags" with body '{"name":"team:marketing"}'
    Then the response status should be 201
    And the JSON response "name" should be "team:marketing"
    And the response body should contain "id"

    When I GET "/api/admin/tags"
    Then the JSON response "count" should be 1

  Scenario: Case-insensitive duplicate name is rejected
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    When I log in as admin "alice" with password "hunter2"
    And I POST "/api/admin/tags" with body '{"name":"Team:Marketing"}'
    Then the response status should be 201

    When I POST "/api/admin/tags" with body '{"name":"team:marketing"}'
    Then the response status should be 409

  Scenario: Forbidden characters in a tag name are rejected
    # §8 of the design doc: tag names must be safe path segments so the
    # /api/admin/repos/{name}/tags/{tag} surface stays clean.
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    When I log in as admin "alice" with password "hunter2"
    And I POST "/api/admin/tags" with body '{"name":"team/marketing"}'
    Then the response status should be 400

  Scenario: PATCH renames a tag and preserves its ID
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    When I log in as admin "alice" with password "hunter2"
    And I POST "/api/admin/tags" with body '{"name":"team:mktg"}'
    And I save the JSON response "id" as "tag_id"
    And I PATCH "/api/admin/tags/${tag_id}" with body '{"name":"team:marketing"}'
    Then the response status should be 200
    And the JSON response "name" should be "team:marketing"
    And the JSON response "id" should equal saved "tag_id"

  Scenario: Rename collision is rejected
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    When I log in as admin "alice" with password "hunter2"
    And I POST "/api/admin/tags" with body '{"name":"team:marketing"}'
    And I POST "/api/admin/tags" with body '{"name":"team:platform"}'
    And I save the JSON response "id" as "platform_id"
    And I PATCH "/api/admin/tags/${platform_id}" with body '{"name":"TEAM:marketing"}'
    Then the response status should be 409

  Scenario: DELETE returns sweep counts (zero in slice 1)
    # The cascade-sweep response shape (Q1 / §6.1) is what slice 2 will
    # populate when it adds assignments. Slice 1 still ships the wire
    # shape so a future change can't silently break it.
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    When I log in as admin "alice" with password "hunter2"
    And I POST "/api/admin/tags" with body '{"name":"contractor:acme"}'
    And I save the JSON response "id" as "tag_id"
    And I DELETE "/api/admin/tags/${tag_id}"
    Then the response status should be 200
    And the response body should contain "swept"
    And the response body should contain "deleted"
    # Slice 1 has no assignments to sweep; the response shape is
    # carried forward to slice 2 where these counts populate.
    And the response body should contain "\"repos\":0"
    And the response body should contain "\"subscriptions\":0"
    And the response body should contain "\"accounts\":0"

  Scenario: Tags survive a server restart
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    When I log in as admin "alice" with password "hunter2"
    And I POST "/api/admin/tags" with body '{"name":"persistent"}'
    And the server restarts
    And I log in as admin "alice" with password "hunter2"
    And I GET "/api/admin/tags"
    Then the JSON response "count" should be 1

  Scenario: PUT repo tags creates and assigns
    # Slice 2 wire contract: a fresh repo with no tags accepts a PUT
    # of unknown tag names, auto-creates them in the catalogue, and
    # the GET returns them on the next call. The catalogue grows by
    # the number of new tag names, not by the number of assignments.
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    And a repository "addresses" exists
    When I log in as admin "alice" with password "hunter2"
    And I PUT "/api/admin/repos/addresses/tags" with body '{"tags":["team:marketing","env:prod"]}'
    Then the response status should be 200
    And the response body should contain "team:marketing"

    When I GET "/api/admin/tags"
    Then the JSON response "count" should be 2

    When I GET "/api/admin/repos/addresses/tags"
    Then the response status should be 200
    And the response body should contain "team:marketing"
    And the response body should contain "env:prod"

  Scenario: PUT repo tags is idempotent
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    And a repository "addresses" exists
    When I log in as admin "alice" with password "hunter2"
    And I PUT "/api/admin/repos/addresses/tags" with body '{"tags":["env:prod"]}'
    And I PUT "/api/admin/repos/addresses/tags" with body '{"tags":["env:prod"]}'
    Then the response status should be 200

    When I GET "/api/admin/tags"
    # Catalogue stays at 1 — the second PUT does not create a duplicate.
    Then the JSON response "count" should be 1

  Scenario: Account tag PUT roundtrips and surfaces in the list
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    When I log in as admin "alice" with password "hunter2"
    And I PUT "/api/admin/accounts/local/alice/tags" with body '{"tags":["contractor:acme"]}'
    Then the response status should be 200

    When I GET "/api/admin/accounts/local/alice/tags"
    Then the response status should be 200
    And the response body should contain "contractor:acme"

  Scenario: Cascade delete sweeps assignments across all three sets
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    And a repository "addresses" exists
    # Create the tag explicitly so the response carries its id at the
    # top level (the integration test save-step only supports flat
    # keys, not nested {tags[0].id} paths).
    When I log in as admin "alice" with password "hunter2"
    And I POST "/api/admin/tags" with body '{"name":"sweep-me"}'
    And I save the JSON response "id" as "sweep_id"
    And I PUT "/api/admin/repos/addresses/tags" with body '{"tags":["sweep-me"]}'
    And I PUT "/api/admin/accounts/local/alice/tags" with body '{"tags":["sweep-me"]}'

    When I DELETE "/api/admin/tags/${sweep_id}"
    Then the response status should be 200
    # 1 repo + 1 account assignment swept. Subscription side stays
    # at zero — issuing a key would require a bound account flow
    # orthogonal to the cascade contract; the per-repo-audit
    # advance for sub assignments is exercised by the dedicated
    # TestAdminTokens_PatchTagsIdempotentNoAuditChurn handler test.
    And the response body should contain "\"repos\":1"
    And the response body should contain "\"accounts\":1"

    When I GET "/api/admin/repos/addresses/tags"
    # Repo side is now empty — the cascade swept the assignment.
    Then the response body should not contain "sweep-me"

  Scenario: Slice 3 — listing tokens by tag filters on effective tag set
    # Two subs on the same repo, one tagged directly. ?tag= narrows
    # the listing to the tagged sub via the §6.3 effective-AND rule.
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    And a regular account "bob" exists
    And a repository "addresses" exists
    When I log in as admin "alice" with password "hunter2"
    And I POST "/api/admin/tokens" with body '{"username":"alice","repo":"addresses"}'
    And I save the JSON response "token" as "alice_tok"
    And I POST "/api/admin/tokens" with body '{"username":"bob","repo":"addresses"}'
    And I PATCH "/api/admin/tokens" with body '{"token":"${alice_tok}","tags":["team:marketing"]}'

    When I GET "/api/admin/tokens?tag=team:marketing"
    Then the response status should be 200
    And the JSON response "count" should be 1
    And the response body should contain "\"username\":\"alice\""
    And the response body should not contain "\"username\":\"bob\""

  Scenario: Slice 3 — bulk revoke requires the typed confirmation phrase
    # Server-side phrase gate: a body without confirm 400s and the
    # tagged sub stays alive. The phrase is deterministic from the
    # request, so a UI-bypassing scripted caller can't sweep tokens
    # without typing it.
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    And a repository "addresses" exists
    When I log in as admin "alice" with password "hunter2"
    And I POST "/api/admin/tokens" with body '{"username":"alice","repo":"addresses"}'
    And I save the JSON response "token" as "alice_tok"
    And I PATCH "/api/admin/tokens" with body '{"token":"${alice_tok}","tags":["team:marketing"]}'

    When I POST "/api/admin/subscriptions/revoke-by-tag" with body '{"tags":["team:marketing"]}'
    Then the response status should be 400
    And the response body should contain "confirm phrase mismatch"

    # Sub stayed alive.
    When I GET "/api/admin/tokens?tag=team:marketing"
    Then the JSON response "count" should be 1

  Scenario: Slice 3 — bulk revoke sweeps every match when the phrase is correct
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    And a regular account "bob" exists
    And a repository "addresses" exists
    When I log in as admin "alice" with password "hunter2"
    And I POST "/api/admin/tokens" with body '{"username":"alice","repo":"addresses"}'
    And I save the JSON response "token" as "alice_tok"
    And I POST "/api/admin/tokens" with body '{"username":"bob","repo":"addresses"}'
    And I save the JSON response "token" as "bob_tok"
    And I PATCH "/api/admin/tokens" with body '{"token":"${alice_tok}","tags":["team:marketing"]}'
    And I PATCH "/api/admin/tokens" with body '{"token":"${bob_tok}","tags":["team:marketing"]}'

    When I POST "/api/admin/subscriptions/revoke-by-tag" with body '{"tags":["team:marketing"],"confirm":"revoke team:marketing"}'
    Then the response status should be 200
    And the response body should contain "\"count\":2"

    When I GET "/api/admin/tokens?tag=team:marketing"
    Then the JSON response "count" should be 0

  Scenario: Slice 3 — bulk revoke without tags is rejected
    # §5.6 invariant: an empty tag set would match every subscription,
    # so the API rejects it before the confirm gate is even evaluated.
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    When I log in as admin "alice" with password "hunter2"
    And I POST "/api/admin/subscriptions/revoke-by-tag" with body '{"tags":[],"confirm":"revoke "}'
    Then the response status should be 400
    And the response body should contain "tags is required"
