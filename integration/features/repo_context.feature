Feature: Per-repo bootstrap context (subscriber)
  As an API client (Formidable, etc.) connecting via a subscription key
  I want a single endpoint that returns who I am, what I can do here,
  and what this repo offers
  So that I can render permission-aware UI without probing per-feature
  endpoints and inferring abilities from 403s.

  # The contract:
  #   - GET /api/repos/{name}/context returns {user, subscription, repo}
  #   - Read-only, repo-scope gated, ability-ungated (abilities are
  #     reported, not enforced — clients render off the report)
  #   - Empty repos surface with empty=true; destinations is a count
  #     summary (total + auto_mirror_enabled), no URLs/credentials
  #     leaked to non-mirror readers.

  Scenario: Bearer caller bootstraps and reads back user, subscription, and repo
    Given the server is running with auth enabled
    And a regular account "alice" exists
    And a repository "addresses" exists
    And a token is issued for user "alice" with repos "addresses"
    And that token has ability "mirror"
    When I request "/api/repos/addresses/context" with that token
    Then the response status should be 200
    And the response body should contain "\"username\":\"alice\""
    And the response body should contain "\"role\":\"regular\""
    And the response body should contain "\"repo\":\"addresses\""
    And the response body should contain "\"abilities\":[\"mirror\"]"
    And the response body should contain "\"empty\":true"
    And the response body should contain "\"is_formidable\":false"
    And the response body should contain "\"total\":0"
    And the response body should contain "\"auto_mirror_enabled\":0"

  Scenario: A no-mirror subscriber still gets the bootstrap (abilities reported, not gated)
    # Without this, a regular subscriber that has no mirror ability
    # would 403 and have no way to render its UI at all. Bootstrap
    # is informational; gates live on the actual mutating endpoints.
    Given the server is running with auth enabled
    And a regular account "alice" exists
    And a repository "addresses" exists
    And a token is issued for user "alice" with repos "addresses"
    When I request "/api/repos/addresses/context" with that token
    Then the response status should be 200
    And the response body should contain "\"abilities\":[]"

  Scenario: Out-of-scope token is 403 on bootstrap
    # Repo scope is the only gate the read path enforces. A token
    # bound to a different repo cannot bootstrap this one regardless
    # of ability bits.
    Given the server is running with auth enabled
    And a regular account "alice" exists
    And a repository "addresses" exists
    And a repository "elsewhere" exists
    And a token is issued for user "alice" with repos "elsewhere"
    When I request "/api/repos/addresses/context" with that token
    Then the response status should be 403

  Scenario: Bootstrap on an unknown repo returns 404
    # The token is in scope (its repo binding matches the URL), but
    # the repo doesn't exist on disk. 404 lets clients distinguish
    # "you can't reach it" from "it isn't there."
    Given the server is running with auth enabled
    And a regular account "alice" exists
    And a token is issued for user "alice" with repos "ghost"
    When I request "/api/repos/ghost/context" with that token
    Then the response status should be 404

  Scenario: Bootstrap surfaces destination counts without leaking URLs
    # A subscriber with mirror sees the same count summary as anyone
    # else — but the bootstrap response intentionally omits per-dest
    # URLs and credentials. Those live behind the destinations API
    # (which the same client can hit separately if it has the bits).
    Given the server is running with auth enabled
    And an admin "alice" exists with password "hunter2"
    And a repository "addresses" exists
    When I log in as admin "alice" with password "hunter2"
    And I POST "/api/admin/credentials" with body '{"name":"c","kind":"pat","secret":"s"}'
    And I POST "/api/admin/repos/addresses/destinations" with body '{"url":"https://github.com/alice/addresses.git","credential_name":"c","enabled":true}'
    And I POST "/api/admin/repos/addresses/destinations" with body '{"url":"https://gitlab.com/alice/addresses.git","credential_name":"c","enabled":false}'
    Given a token is issued for user "alice" with repos "addresses"
    And that token has ability "mirror"
    When I request "/api/repos/addresses/context" with that token
    Then the response status should be 200
    And the response body should contain "\"total\":2"
    And the response body should contain "\"auto_mirror_enabled\":1"
    And the response body should not contain "github.com/alice/addresses.git"
    And the response body should not contain "gitlab.com/alice/addresses.git"

  Scenario: Full Formidable connect flow — bootstrap then act on what it returned
    # End-to-end shape a Formidable client follows on connect:
    #   1. /context — single bootstrap call; learn role + abilities
    #      + repo state. Render UI off this response.
    #   2. /destinations — the Mirror Destinations panel populates
    #      from this list (already known to be allowed because the
    #      bootstrap reported the mirror ability).
    #   3. /destinations/{id}/sync — manual mirror trigger only fires
    #      when the user clicks; the bootstrap told the UI it could
    #      offer the button.
    # Pinning all three steps in one scenario locks in the contract
    # at the seam where it actually matters: a real client session.
    Given the server is running with auth enabled
    And an admin "alice" exists with password "hunter2"
    And a repository "addresses" exists
    When I log in as admin "alice" with password "hunter2"
    And I POST "/api/admin/credentials" with body '{"name":"c","kind":"pat","secret":"s"}'
    And I POST "/api/admin/repos/addresses/destinations" with body '{"url":"https://github.com/alice/addresses.git","credential_name":"c","enabled":false}'
    And I save the JSON response "id" as "dest_id"
    Given a token is issued for user "alice" with repos "addresses"
    And that token has ability "mirror"

    # Step 1 — bootstrap.
    When I request "/api/repos/addresses/context" with that token
    Then the response status should be 200
    And the response body should contain "\"abilities\":[\"mirror\"]"
    And the response body should contain "\"total\":1"
    And the response body should contain "\"auto_mirror_enabled\":0"

    # Step 2 — destinations panel populates (read split lets us hit
    # this with the same token; the bootstrap's mirror=true above is
    # what told the client it could).
    When I request "/api/repos/addresses/destinations" with that token
    Then the response status should be 200
    And the JSON response "count" should be 1

    # Step 3 — the manual /sync the bootstrap told the UI it could
    # offer. Reaches the credential-vault path; the test stub will
    # return non-2xx for the actual outbound push, but the gate on
    # the route itself must pass for a mirror-able subscriber.
    When I POST "/api/repos/addresses/destinations/${dest_id}/sync" with that token
    Then the response status should not be 401
    And the response status should not be 403
