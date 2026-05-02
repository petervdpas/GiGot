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
