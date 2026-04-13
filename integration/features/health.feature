Feature: Health check
  As a client connecting to GiGot
  I want to verify the server is running
  So that I know it's safe to proceed with sync

  Scenario: Server responds to health check
    Given the server is running
    When I request "/api/health"
    Then the response status should be 200
    And the response should contain JSON key "status" with value "ok"
