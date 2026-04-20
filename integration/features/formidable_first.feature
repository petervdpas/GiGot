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

  Scenario: Clone of a pre-marked upstream preserves the marker and fills missing scaffold
    Given the server is running in formidable-first mode
    And a local git source "src-marked" exists with a formidable marker
    When I POST "/api/repos" with body '{"name":"clone-idem","source_url":"${src-marked}"}'
    Then the response status should be 201
    # Source has README + marker = 2 commits. Clone adds one shape-fill
    # commit for the missing templates/ and storage/ starter pieces.
    And the repository "clone-idem" has 3 commits
    And the repository "clone-idem" contains file ".formidable/context.json"
    And the repository "clone-idem" contains file "templates/basic.yaml"
    And the repository "clone-idem" contains file "storage/.gitkeep"
    # Proves the existing marker was NOT rewritten — its scaffolded_at
    # is still the source's fixed 2024-01-01 timestamp, not today.
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

  Scenario: Convert a plain repo to a Formidable context (formidable_first mode)
    Given the server is running in formidable-first mode
    And an admin "alice" exists with password "hunter2"
    And a local git source "src-plain2" exists
    When I log in as admin "alice" with password "hunter2"
    And I POST "/api/repos" with body '{"name":"to-convert","source_url":"${src-plain2}","scaffold_formidable":false}'
    Then the response status should be 201
    And the repository "to-convert" does not contain file ".formidable/context.json"
    When I POST "/api/admin/repos/to-convert/formidable" with body ''
    Then the response status should be 200
    And the JSON response "stamped" should be true
    And the repository "to-convert" contains file ".formidable/context.json"
    And the repository "to-convert" file ".formidable/context.json" is valid JSON with field "version" equal to "1"
    # Convert must also fill in the templates/ and storage/ starter
    # layout so the repo is actually usable as a Formidable context,
    # not just stamped with a marker.
    And the repository "to-convert" contains file "templates/basic.yaml"
    And the repository "to-convert" contains file "storage/.gitkeep"
    And the top audit event in repo "to-convert" has type "repo_convert_formidable"

  Scenario: Converting an already-Formidable repo is idempotent
    Given the server is running in formidable-first mode
    And an admin "alice" exists with password "hunter2"
    When I log in as admin "alice" with password "hunter2"
    And I POST "/api/repos" with body '{"name":"already-formidable"}'
    Then the response status should be 201
    When I POST "/api/admin/repos/already-formidable/formidable" with body ''
    Then the response status should be 200
    And the JSON response "stamped" should be false

  Scenario: Convert is rejected when the server is not in formidable-first mode
    Given the server is running
    And an admin "alice" exists with password "hunter2"
    When I log in as admin "alice" with password "hunter2"
    And I POST "/api/repos" with body '{"name":"plain-generic"}'
    Then the response status should be 201
    When I POST "/api/admin/repos/plain-generic/formidable" with body ''
    Then the response status should be 403
    And the response body should contain "formidable_first"

  Scenario: Convert on an empty repo returns 422
    Given the server is running in formidable-first mode
    And an admin "alice" exists with password "hunter2"
    And a repository "bare-and-empty" exists
    When I log in as admin "alice" with password "hunter2"
    And I POST "/api/admin/repos/bare-and-empty/formidable" with body ''
    Then the response status should be 422
    And the response body should contain "empty"

  Scenario: Convert without a session is rejected (401)
    Given the server is running in formidable-first mode
    When I POST "/api/repos" with body '{"name":"unauthed-convert"}'
    And I POST "/admin/logout" with body ''
    And I POST "/api/admin/repos/unauthed-convert/formidable" with body ''
    Then the response status should be 401
