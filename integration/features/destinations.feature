Feature: Mirror-sync destinations (admin)
  As an administrator
  I want to attach outbound mirror destinations to a repo, each pointing at
  a named credential in the vault
  So that the credential vault's link to the mirror-sync worker (design doc
  §5) is wired end-to-end before any push actually fires

  Scenario: Listing destinations requires a session
    Given the server is running
    And a repository "addresses" exists
    When I GET "/api/admin/repos/addresses/destinations"
    Then the response status should be 401

  Scenario: Unknown repo returns 404
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    When I log in as admin "alice" with password "hunter2"
    And I GET "/api/admin/repos/does-not-exist/destinations"
    Then the response status should be 404

  Scenario: Admin can attach, list, and remove a destination
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    And a repository "addresses" exists
    When I log in as admin "alice" with password "hunter2"
    And I POST "/api/admin/credentials" with body '{"name":"github-personal","kind":"pat","secret":"ghp_abc"}'
    Then the response status should be 201

    When I POST "/api/admin/repos/addresses/destinations" with body '{"url":"https://github.com/alice/addresses.git","credential_name":"github-personal"}'
    Then the response status should be 201
    And the JSON response "credential_name" should be "github-personal"
    And the JSON response "id" should not be empty
    And I save the JSON response "id" as "dest_id"

    When I GET "/api/admin/repos/addresses/destinations"
    Then the JSON response "count" should be 1

    When I DELETE "/api/admin/repos/addresses/destinations/${dest_id}"
    Then the response status should be 204

    When I GET "/api/admin/repos/addresses/destinations"
    Then the JSON response "count" should be 0

  Scenario: Creating a destination with an unknown credential is rejected
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    And a repository "addresses" exists
    When I log in as admin "alice" with password "hunter2"
    And I POST "/api/admin/repos/addresses/destinations" with body '{"url":"https://github.com/alice/addresses.git","credential_name":"nonexistent"}'
    Then the response status should be 404

  Scenario: Creating a destination without a URL is rejected
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    And a repository "addresses" exists
    When I log in as admin "alice" with password "hunter2"
    And I POST "/api/admin/credentials" with body '{"name":"c","kind":"pat","secret":"s"}'
    And I POST "/api/admin/repos/addresses/destinations" with body '{"credential_name":"c"}'
    Then the response status should be 400

  Scenario: Enabled flag toggles both ways via PATCH and survives a URL-only PATCH
    # Proves the click-to-toggle badge in the admin UI stays honest
    # end-to-end: disable → re-enable cycles work, and an edit-form
    # PATCH that only changes `url` must NOT reset the stored enabled
    # flag. The admin UI relies on "nil pointer means unchanged"
    # semantics on the UpdateDestinationRequest.
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    And a repository "addresses" exists
    When I log in as admin "alice" with password "hunter2"
    And I POST "/api/admin/credentials" with body '{"name":"c","kind":"pat","secret":"s"}'
    And I POST "/api/admin/repos/addresses/destinations" with body '{"url":"https://x","credential_name":"c"}'
    And I save the JSON response "id" as "dest_id"

    # Create path defaults to enabled=true even though the body omitted it.
    Then the JSON response "enabled" should be true

    # Click-to-toggle: disable.
    When I PATCH "/api/admin/repos/addresses/destinations/${dest_id}" with body '{"enabled":false}'
    Then the response status should be 200
    And the JSON response "enabled" should be false

    # URL-only PATCH must NOT silently re-enable the destination.
    When I PATCH "/api/admin/repos/addresses/destinations/${dest_id}" with body '{"url":"https://y"}'
    Then the response status should be 200
    And the JSON response "enabled" should be false
    And the JSON response "url" should be "https://y"

    # Click-to-toggle: re-enable.
    When I PATCH "/api/admin/repos/addresses/destinations/${dest_id}" with body '{"enabled":true}'
    Then the response status should be 200
    And the JSON response "enabled" should be true

  Scenario: PATCH disables a destination without rewriting its id
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    And a repository "addresses" exists
    When I log in as admin "alice" with password "hunter2"
    And I POST "/api/admin/credentials" with body '{"name":"c","kind":"pat","secret":"s"}'
    And I POST "/api/admin/repos/addresses/destinations" with body '{"url":"https://x","credential_name":"c"}'
    And I save the JSON response "id" as "dest_id"
    And I PATCH "/api/admin/repos/addresses/destinations/${dest_id}" with body '{"enabled":false}'
    Then the response status should be 200
    And the JSON response "id" should equal saved "dest_id"

  Scenario: Deleting a credential referenced by a destination is blocked with 409
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    And a repository "addresses" exists
    When I log in as admin "alice" with password "hunter2"
    And I POST "/api/admin/credentials" with body '{"name":"github-personal","kind":"pat","secret":"ghp_abc"}'
    And I POST "/api/admin/repos/addresses/destinations" with body '{"url":"https://x","credential_name":"github-personal"}'
    And I save the JSON response "id" as "dest_id"
    And I DELETE "/api/admin/credentials/github-personal"
    Then the response status should be 409
    And the response body should contain "addresses"

    # After clearing the reference, the credential can be removed.
    When I DELETE "/api/admin/repos/addresses/destinations/${dest_id}"
    Then the response status should be 204
    When I DELETE "/api/admin/credentials/github-personal"
    Then the response status should be 204

  Scenario: Deleting a repo cascades to its destinations
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    And a repository "addresses" exists
    When I log in as admin "alice" with password "hunter2"
    And I POST "/api/admin/credentials" with body '{"name":"c","kind":"pat","secret":"s"}'
    And I POST "/api/admin/repos/addresses/destinations" with body '{"url":"https://x","credential_name":"c"}'
    And I DELETE "/api/repos/addresses"
    Then the response status should be 204

    # Credential is no longer referenced — delete should now succeed.
    When I DELETE "/api/admin/credentials/c"
    Then the response status should be 204

  Scenario: Subscriber with mirror ability can list destinations via the subscriber API
    Given the server is running with auth enabled
    And a repository "addresses" exists
    And a token is issued for user "alice" with repos "addresses"
    And that token has ability "mirror"
    When I request "/api/repos/addresses/destinations" with that token
    Then the response status should be 200

  Scenario: Subscriber without mirror ability can READ the destinations list
    # Read/write split (see handler_repo_destinations.go): a token in
    # repo scope without the mirror ability gets the informational list.
    # Lets a Formidable client know "destinations are configured" even
    # for a subscriber that can't manage them.
    Given the server is running with auth enabled
    And a repository "addresses" exists
    And a token is issued for user "alice" with repos "addresses"
    When I request "/api/repos/addresses/destinations" with that token
    Then the response status should be 200

  Scenario: Subscriber without mirror ability can READ a single destination by id
    # Read split applies to the per-id GET as well, not just the
    # list. A no-mirror token in scope can read either shape; a
    # future refactor that re-tightens one of them will fail here.
    Given the server is running with auth enabled
    And an admin "alice" exists with password "hunter2"
    And a repository "addresses" exists
    When I log in as admin "alice" with password "hunter2"
    And I POST "/api/admin/credentials" with body '{"name":"c","kind":"pat","secret":"s"}'
    And I POST "/api/admin/repos/addresses/destinations" with body '{"url":"https://x","credential_name":"c"}'
    And I save the JSON response "id" as "dest_id"
    Given a token is issued for user "alice" with repos "addresses"
    When I request "/api/repos/addresses/destinations/${dest_id}" with that token
    Then the response status should be 200
    And the JSON response "id" should equal saved "dest_id"

  Scenario: Subscriber without mirror ability is 403 on PATCH
    # Writes (POST/PATCH/DELETE/sync) keep the role+ability fence even
    # though reads dropped it. PATCH is exercised independently from
    # POST/DELETE/sync so a one-off relaxation of any single verb's
    # gate doesn't slip past CI.
    Given the server is running with auth enabled
    And an admin "alice" exists with password "hunter2"
    And a repository "addresses" exists
    When I log in as admin "alice" with password "hunter2"
    And I POST "/api/admin/credentials" with body '{"name":"c","kind":"pat","secret":"s"}'
    And I POST "/api/admin/repos/addresses/destinations" with body '{"url":"https://x","credential_name":"c"}'
    And I save the JSON response "id" as "dest_id"
    Given a token is issued for user "alice" with repos "addresses"
    When I PATCH "/api/repos/addresses/destinations/${dest_id}" with body '{"enabled":false}' with that token
    Then the response status should be 403

  Scenario: Subscriber without mirror ability is 403 on DELETE
    Given the server is running with auth enabled
    And an admin "alice" exists with password "hunter2"
    And a repository "addresses" exists
    When I log in as admin "alice" with password "hunter2"
    And I POST "/api/admin/credentials" with body '{"name":"c","kind":"pat","secret":"s"}'
    And I POST "/api/admin/repos/addresses/destinations" with body '{"url":"https://x","credential_name":"c"}'
    And I save the JSON response "id" as "dest_id"
    Given a token is issued for user "alice" with repos "addresses"
    When I DELETE "/api/repos/addresses/destinations/${dest_id}" with that token
    Then the response status should be 403

  Scenario: Out-of-scope token is 403 on a single-destination read
    # The mirror gate dropped on reads, but the repo-scope gate did
    # not. A token bound to a different repo cannot reach this repo's
    # destinations, regardless of ability.
    Given the server is running with auth enabled
    And an admin "alice" exists with password "hunter2"
    And a regular account "bob" exists
    And a repository "addresses" exists
    And a repository "elsewhere" exists
    When I log in as admin "alice" with password "hunter2"
    And I POST "/api/admin/credentials" with body '{"name":"c","kind":"pat","secret":"s"}'
    And I POST "/api/admin/repos/addresses/destinations" with body '{"url":"https://x","credential_name":"c"}'
    And I save the JSON response "id" as "dest_id"
    Given a token is issued for user "bob" with repos "elsewhere"
    When I request "/api/repos/addresses/destinations/${dest_id}" with that token
    Then the response status should be 403

  Scenario: Subscriber without mirror ability is 403 on the /sync route
    # Writes (POST/PATCH/DELETE/sync) keep the role+ability fence even
    # though reads dropped it. /sync is a write — it triggers an
    # outbound mirror push.
    Given the server is running with auth enabled
    And a repository "addresses" exists
    And a token is issued for user "alice" with repos "addresses"
    When I POST "/api/repos/addresses/destinations/any-id/sync" with that token
    Then the response status should be 403

  Scenario: Unknown destination id on /sync returns 404 for an admin
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    And a repository "addresses" exists
    When I log in as admin "alice" with password "hunter2"
    And I POST "/api/admin/repos/addresses/destinations/does-not-exist/sync" with body '{}'
    Then the response status should be 404

  Scenario: Destinations survive a server restart
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    And a repository "addresses" exists
    When I log in as admin "alice" with password "hunter2"
    And I POST "/api/admin/credentials" with body '{"name":"c","kind":"pat","secret":"s"}'
    And I POST "/api/admin/repos/addresses/destinations" with body '{"url":"https://x","credential_name":"c"}'
    And the server restarts
    And I log in as admin "alice" with password "hunter2"
    And I GET "/api/admin/repos/addresses/destinations"
    Then the JSON response "count" should be 1
