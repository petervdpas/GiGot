Feature: Index page
  As an administrator
  I want to see a status page
  So that I can verify GiGot is configured correctly

  Scenario: Index page is accessible
    Given the server is running
    When I request "/"
    Then the response status should be 200
    And the response content type should contain "text/html"
    And the response body should contain "GiGot"

  Scenario: Index page shows repo count
    Given the server is running
    And a repository "project-alpha" exists
    And a repository "project-beta" exists
    When I request "/"
    Then the response status should be 200
    And the response body should contain "2"

  Scenario: Unknown paths return 404
    Given the server is running
    When I request "/does-not-exist"
    Then the response status should be 404
