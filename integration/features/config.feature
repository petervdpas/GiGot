Feature: Configuration
  As an administrator
  I want to configure GiGot with a JSON file
  So that I can control server behavior

  Scenario: Server starts with default config
    Given no config file exists
    When the config is loaded
    Then the server port should be 3417
    And the repo root should be "./repos"

  Scenario: Server loads custom config
    Given a config file with port 9000
    When the config is loaded
    Then the server port should be 9000

  Scenario: Partial config merges with defaults
    Given a config file with only logging level "debug"
    When the config is loaded
    Then the server port should be 3417
    And the logging level should be "debug"

  Scenario: Generate default config file
    Given no config file exists
    When I generate a default config
    Then a "gigot.json" file should exist
    And loading that config should have port 3417
