Feature: Self-serve /api/me
  As a signed-in user (admin or regular)
  I want an endpoint that returns my profile and my subscription keys
  So that I can retrieve the keys I need to configure a Formidable
  client without involving an administrator out of band.

  Scenario: /api/me rejects unauthenticated callers
    Given the server is running
    When I GET "/api/me"
    Then the response status should be 401

  Scenario: Admin can fetch their own profile and subscription keys
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    And a repository "addresses" exists
    When I log in as admin "alice" with password "hunter2"
    And I POST "/api/admin/tokens" with body '{"username":"alice","repo":"addresses"}'
    Then the response status should be 201
    When I GET "/api/me"
    Then the response status should be 200
    And the JSON response "username" should be "alice"
    And the JSON response "provider" should be "local"
    And the JSON response "role" should be "admin"
    And the response body should contain "\"username\":\"alice\""

  Scenario: /api/me carries the email an admin set on the caller's account
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    When I log in as admin "alice" with password "hunter2"
    And I PATCH "/api/admin/accounts/local/alice" with body '{"email":"alice@example.com"}'
    Then the response status should be 200
    When I GET "/api/me"
    Then the response status should be 200
    And the JSON response "email" should be "alice@example.com"

  Scenario: Bearer caller gets profile + their single subscription via /api/me
    # /api/me also accepts bearer auth so an API client (e.g. a
    # Formidable instance) can introspect its own role + abilities
    # without probing 403s. The bearer-flavoured response surfaces
    # only the single token presented — the caller can't enumerate
    # sibling keys it doesn't already hold.
    Given the server is running with auth enabled
    And a regular account "alice" exists
    And a repository "repo-a" exists
    And a token is issued for user "alice" with repos "repo-a"
    And that token has ability "mirror"
    When I request "/api/me" with that token
    Then the response status should be 200
    And the JSON response "username" should be "alice"
    And the JSON response "provider" should be "local"
    And the JSON response "role" should be "regular"
    And the response body should contain "\"repo\":\"repo-a\""
    And the response body should contain "\"abilities\":[\"mirror\"]"

  Scenario: Bearer for a scoped username surfaces the right account
    # The realistic OAuth shape — token Username "github:alice@..."
    # must resolve to the (github, alice@...) account and surface
    # that account's profile + role. Without this, only legacy
    # bare-string tokens would work for bearer /me.
    Given the server is running with auth enabled
    And a regular account "alice@example.com" exists on provider "github"
    And a repository "repo-a" exists
    And a token is issued for user "github:alice@example.com" on repo "repo-a"
    When I request "/api/me" with that token
    Then the response status should be 200
    And the JSON response "username" should be "github:alice@example.com"
    And the JSON response "provider" should be "github"
    And the JSON response "role" should be "regular"

  Scenario: Bearer caller only sees their own subscription, not siblings
    # Two tokens exist for two different accounts; alice's bearer
    # /me must not leak bob's subscription.
    Given the server is running with auth enabled
    And a regular account "alice" exists
    And a regular account "bob" exists
    And a repository "repo-a" exists
    And a repository "repo-b" exists
    And a token is issued for user "alice" with repos "repo-a"
    And the admin issues another key for "bob" on repo "repo-b"
    When I request "/api/me" with that token
    Then the response status should be 200
    And the response body should contain "\"username\":\"alice\""
    And the response body should not contain "\"username\":\"bob\""

  Scenario: Session takes precedence when both cookie and bearer are presented
    # The cookie-bearing browser path stays canonical for /api/me —
    # an unrelated bearer in the same request must not flip the
    # response to the bearer-filtered view.
    Given the server is running with auth enabled
    And an admin "alice" exists with password "hunter2"
    And a regular account "bob" exists
    And a repository "repo-a" exists
    And a token is issued for user "bob" with repos "repo-a"
    When I log in as admin "alice" with password "hunter2"
    And I request "/api/me" with that token
    Then the response status should be 200
    And the JSON response "username" should be "alice"
    And the JSON response "role" should be "admin"
    And the response body should not contain "\"username\":\"bob\""

  Scenario: /api/me filters to the caller's own subscriptions only
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    And a regular account "bob" exists
    And a repository "addresses" exists
    And a repository "projects" exists
    When I log in as admin "alice" with password "hunter2"
    And I POST "/api/admin/tokens" with body '{"username":"alice","repo":"addresses"}'
    And I POST "/api/admin/tokens" with body '{"username":"bob","repo":"projects"}'
    And I GET "/api/me"
    Then the response status should be 200
    And the response body should contain "\"username\":\"alice\""
    And the response body should not contain "\"username\":\"bob\""
