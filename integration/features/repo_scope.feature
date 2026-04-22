Feature: Subscription keys are scoped to one repository
  As an administrator
  I want each subscription key to bind to exactly one repository
  So that a leaked key only exposes that one repo, and revocation is
  a single-row operation.

  Scenario: Token with its bound repo can read it
    Given the server is running with auth enabled
    And a repository "assigned-repo" exists
    And a token is issued for user "alice" on repo "assigned-repo"
    When I request "/api/repos/assigned-repo" with that token
    Then the response status should be 200

  Scenario: Token cannot access a repo it is not bound to
    Given the server is running with auth enabled
    And a repository "allowed" exists
    And a repository "not-allowed" exists
    And a token is issued for user "alice" on repo "allowed"
    When I request "/api/repos/not-allowed" with that token
    Then the response status should be 403

  Scenario: Listing repos returns only the token's bound repo
    Given the server is running with auth enabled
    And a repository "alpha" exists
    And a repository "beta" exists
    And a repository "gamma" exists
    And a token is issued for user "alice" on repo "alpha"
    When I request "/api/repos" with that token
    Then the response status should be 200
    And the JSON response "count" should be 1

  Scenario: Git clone is denied for a repo the token is not bound to
    Given the server is running with auth enabled
    And a repository "private-repo" exists
    And a repository "other-repo" exists
    And a token is issued for user "alice" on repo "other-repo"
    When I request "/git/private-repo.git/info/refs?service=git-upload-pack" with that token
    Then the response status should be 403

  Scenario: Git clone succeeds for the token's bound repo
    Given the server is running with auth enabled
    And a repository "shared-repo" exists
    And a token is issued for user "alice" on repo "shared-repo"
    When I request "/git/shared-repo.git/info/refs?service=git-upload-pack" with that token
    Then the response status should be 200

  Scenario: Admin can rebind an existing token to a different repo
    Given the server is running with auth enabled
    And a repository "old-repo" exists
    And a repository "new-repo" exists
    And a token is issued for user "alice" on repo "old-repo"
    When the admin rebinds that token to "new-repo"
    And I request "/api/repos/old-repo" with that token
    Then the response status should be 403

    When I request "/api/repos/new-repo" with that token
    Then the response status should be 200

  Scenario: A second key for the same (user, repo) is rejected
    Given the server is running
    And a regular account "alice" exists
    And a repository "single-scope" exists
    When I POST "/api/auth/token" with body '{"username":"alice","repo":"single-scope"}'
    Then the response status should be 201
    When I POST "/api/auth/token" with body '{"username":"alice","repo":"single-scope"}'
    Then the response status should be 409
