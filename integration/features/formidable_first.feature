Feature: Config-driven marker provisioning (server.formidable_first)
  As an operator running GiGot in Formidable-first mode
  I want POST /api/repos to stamp the Formidable marker by default
  So that newly-created or cloned repos are usable as Formidable contexts
  without clients having to pass scaffold_formidable on every call.

  # The feature covers the server-mode branch of the §2.7 decision matrix
  # that the handler-level TestCreateRepoStampingMatrix also covers, lifted
  # up so an end-to-end scenario can exercise the same behaviour through
  # the real HTTP pipeline.

  Scenario: Init on a formidable-first server stamps the marker by default
    Given the server is running in formidable-first mode
    When I POST "/api/repos" with body '{"name":"init-default"}'
    Then the response status should be 201
    And the repository "init-default" has commits
    And the repository "init-default" contains file ".formidable/context.json"
    And the repository "init-default" file ".formidable/context.json" is valid JSON with field "version" equal to "1"

  Scenario: Explicit scaffold_formidable=false on init overrides the server default (escape hatch)
    Given the server is running in formidable-first mode
    When I POST "/api/repos" with body '{"name":"init-optout","scaffold_formidable":false}'
    Then the response status should be 201
    And the repository "init-optout" has no commits

  Scenario: Clone of a plain upstream on a formidable-first server stamps a marker
    Given the server is running in formidable-first mode
    And a local git source "src-plain" exists
    When I POST "/api/repos" with body '{"name":"clone-default","source_url":"${src-plain}"}'
    Then the response status should be 201
    And the repository "clone-default" has 2 commits
    And the repository "clone-default" contains file ".formidable/context.json"
    And the repository "clone-default" head commit is authored by "GiGot Scaffolder"

  Scenario: Clone of a broken-marker upstream on a formidable-first server overwrites it with a valid marker
    Given the server is running in formidable-first mode
    And a local git source "src-broken" exists with a broken formidable marker
    When I POST "/api/repos" with body '{"name":"clone-fix","source_url":"${src-broken}"}'
    Then the response status should be 201
    # Source had 2 commits (readme + broken marker); stamp adds exactly
    # one overwrite commit, so the cloned repo ends up at 3.
    And the repository "clone-fix" has 3 commits
    And the repository "clone-fix" contains file ".formidable/context.json"
    And the repository "clone-fix" file ".formidable/context.json" is valid JSON with field "version" equal to "1"
    And the repository "clone-fix" file ".formidable/context.json" is valid JSON with field "scaffolded_by" equal to "gigot"
    And the repository "clone-fix" head commit is authored by "GiGot Scaffolder"

  Scenario: Clone of a pre-marked upstream on a formidable-first server preserves the marker (idempotent)
    Given the server is running in formidable-first mode
    And a local git source "src-marked" exists with a formidable marker
    When I POST "/api/repos" with body '{"name":"clone-idem","source_url":"${src-marked}"}'
    Then the response status should be 201
    And the repository "clone-idem" has 2 commits
    And the repository "clone-idem" contains file ".formidable/context.json"
    # Proves idempotence at the wire layer: the marker is the source's,
    # not a fresh one the server just wrote (which would have today's
    # timestamp, not the fixed 2024-01-01 one the seeder commits).
    And the repository "clone-idem" file ".formidable/context.json" is valid JSON with field "scaffolded_at" equal to "2024-01-01T00:00:00Z"

  Scenario: Explicit scaffold_formidable=false on clone overrides the server default (escape hatch)
    Given the server is running in formidable-first mode
    And a local git source "src-plain2" exists
    When I POST "/api/repos" with body '{"name":"clone-optout","source_url":"${src-plain2}","scaffold_formidable":false}'
    Then the response status should be 201
    And the repository "clone-optout" has 1 commits
    And the repository "clone-optout" does not contain file ".formidable/context.json"

  Scenario: Generic server does not stamp clones by default
    Given the server is running
    And a local git source "src-generic" exists
    When I POST "/api/repos" with body '{"name":"clone-generic","source_url":"${src-generic}"}'
    Then the response status should be 201
    And the repository "clone-generic" has 1 commits
    And the repository "clone-generic" does not contain file ".formidable/context.json"

  Scenario: Generic server stamps a clone when the request opts in explicitly
    Given the server is running
    And a local git source "src-optin" exists
    When I POST "/api/repos" with body '{"name":"clone-optin","source_url":"${src-optin}","scaffold_formidable":true}'
    Then the response status should be 201
    And the repository "clone-optin" has 2 commits
    And the repository "clone-optin" contains file ".formidable/context.json"
