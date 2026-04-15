Feature: Policy-gated access
  As a GiGot operator
  I want all protected endpoints to consult a single policy evaluator
  So that per-repo access is enforced consistently across HTTP + git routes

  Scenario: Default policy denies token callers who have no repos assigned
    Given the server is running with auth enabled
    And a token is issued for user "alice"
    When I request "/api/repos" with that token
    Then the response status should be 403

  Scenario: Swapping in a deny-all policy blocks even authenticated callers
    Given the server is running with auth enabled
    And the policy is deny-all
    And a token is issued for user "alice"
    When I request "/api/repos" with that token
    Then the response status should be 403

  Scenario: Deny-all policy blocks git clone attempts
    Given the server is running with auth enabled
    And a repository "secret-repo" exists
    And the policy is deny-all
    And a token is issued for user "alice"
    When I request "/git/secret-repo.git/info/refs?service=git-upload-pack" with that token
    Then the response status should be 403
