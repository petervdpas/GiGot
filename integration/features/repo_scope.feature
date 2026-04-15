Feature: Subscription keys are scoped to specific repositories
  As an administrator
  I want to assign each subscription key to a specific set of repositories
  So that a leaked key cannot access unrelated repos

  Scenario: Token with an assigned repo can read it
    Given the server is running with auth enabled
    And a repository "assigned-repo" exists
    And a token is issued for user "alice" with repos "assigned-repo"
    When I request "/api/repos/assigned-repo" with that token
    Then the response status should be 200

  Scenario: Token without the repo is denied on read
    Given the server is running with auth enabled
    And a repository "other-repo" exists
    And a token is issued for user "alice" with repos ""
    When I request "/api/repos/other-repo" with that token
    Then the response status should be 403

  Scenario: Token cannot access a repo outside its allowlist
    Given the server is running with auth enabled
    And a repository "allowed" exists
    And a repository "not-allowed" exists
    And a token is issued for user "alice" with repos "allowed"
    When I request "/api/repos/not-allowed" with that token
    Then the response status should be 403

  Scenario: Listing repos returns only the token's assigned set
    Given the server is running with auth enabled
    And a repository "alpha" exists
    And a repository "beta" exists
    And a repository "gamma" exists
    And a token is issued for user "alice" with repos "alpha,gamma"
    When I request "/api/repos" with that token
    Then the response status should be 200
    And the JSON response "count" should be 2

  Scenario: Git clone attempt is denied for an unassigned repo
    Given the server is running with auth enabled
    And a repository "private-repo" exists
    And a token is issued for user "alice" with repos ""
    When I request "/git/private-repo.git/info/refs?service=git-upload-pack" with that token
    Then the response status should be 403

  Scenario: Git clone attempt succeeds for an assigned repo
    Given the server is running with auth enabled
    And a repository "shared-repo" exists
    And a token is issued for user "alice" with repos "shared-repo"
    When I request "/git/shared-repo.git/info/refs?service=git-upload-pack" with that token
    Then the response status should be 200

  Scenario: Admin can re-scope an existing token to different repos
    Given the server is running with auth enabled
    And a repository "old-repo" exists
    And a repository "new-repo" exists
    And a token is issued for user "alice" with repos "old-repo"
    When the admin rescopes that token to "new-repo"
    And I request "/api/repos/old-repo" with that token
    Then the response status should be 403

    When I request "/api/repos/new-repo" with that token
    Then the response status should be 200
