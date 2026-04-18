Feature: Binary image transport and record queries (Phases F3, F4)
  As a Formidable client whose records reference images and whose
  lists render queries over many records
  I want the server to transport binary blobs under
  storage/<template>/images/ and to answer record queries at HEAD
  So that image uploads and filtered lists work without extra
  client-side indexing.

  # Covers §10.5 (descoped to transport-only), §10.8, and §11 F3/F4.
  # F3: binary files under storage/<template>/images/ round-trip and
  #     same-path overwrite is accepted (no 409).
  # F4: GET /records/{template} returns parsed records at HEAD, with
  #     optional equality/range filter, sort, and limit.

  Scenario: A binary image blob round-trips through the sync API
    Given the server is running in formidable-first mode
    When I POST "/api/repos" with body '{"name":"img-xfer"}'
    Then the response status should be 201
    And I GET "/api/repos/img-xfer/head"
    And I save the JSON response "version" as "head0"
    When I put binary file "storage/addresses/images/photo.jpg" in repo "img-xfer" with bytes "89504e470d0a1a0a" and parent "${head0}"
    Then the response status should be 200
    And I GET "/api/repos/img-xfer/head"
    And I save the JSON response "version" as "head1"
    When I GET "/api/repos/img-xfer/files/storage/addresses/images/photo.jpg"
    Then the response status should be 200
    And the response body base64-decodes to hex "89504e470d0a1a0a"

  Scenario: Overwriting the same image path accepts the new bytes without conflict
    Given the server is running in formidable-first mode
    When I POST "/api/repos" with body '{"name":"img-overwrite"}'
    Then the response status should be 201
    And I GET "/api/repos/img-overwrite/head"
    And I save the JSON response "version" as "head0"
    When I put binary file "storage/addresses/images/photo.jpg" in repo "img-overwrite" with bytes "00ff00ff" and parent "${head0}"
    Then the response status should be 200
    And I GET "/api/repos/img-overwrite/head"
    And I save the JSON response "version" as "head1"
    When I put binary file "storage/addresses/images/photo.jpg" in repo "img-overwrite" with bytes "ffeeddcc" and parent "${head1}"
    Then the response status should be 200
    And I GET "/api/repos/img-overwrite/files/storage/addresses/images/photo.jpg"
    And the response status should be 200
    And the response body base64-decodes to hex "ffeeddcc"

  Scenario: Record query returns all records of a template
    Given the server is running in formidable-first mode
    When I POST "/api/repos" with body '{"name":"rq-all"}'
    Then the response status should be 201
    And I GET "/api/repos/rq-all/head"
    And I save the JSON response "version" as "head0"
    And I put a record "storage/addresses/oak.meta.json" in repo "rq-all" with data '{"city":"London","count":7}' updated "2025-01-01T00:00:00Z" and parent "${head0}"
    And the response status should be 200
    And I GET "/api/repos/rq-all/head"
    And I save the JSON response "version" as "head1"
    And I put a record "storage/addresses/elm.meta.json" in repo "rq-all" with data '{"city":"Paris","count":3}' updated "2025-01-02T00:00:00Z" and parent "${head1}"
    And the response status should be 200
    When I GET "/api/repos/rq-all/records/addresses"
    Then the response status should be 200
    And the records response contains 2 records

  Scenario: Record query filters, sorts, and limits
    Given the server is running in formidable-first mode
    When I POST "/api/repos" with body '{"name":"rq-filter"}'
    Then the response status should be 201
    And I GET "/api/repos/rq-filter/head"
    And I save the JSON response "version" as "head0"
    And I put a record "storage/addresses/oak.meta.json" in repo "rq-filter" with data '{"city":"London","count":7}' updated "2025-01-01T00:00:00Z" and parent "${head0}"
    And the response status should be 200
    And I GET "/api/repos/rq-filter/head"
    And I save the JSON response "version" as "head1"
    And I put a record "storage/addresses/elm.meta.json" in repo "rq-filter" with data '{"city":"Paris","count":3}' updated "2025-01-02T00:00:00Z" and parent "${head1}"
    And the response status should be 200
    And I GET "/api/repos/rq-filter/head"
    And I save the JSON response "version" as "head2"
    And I put a record "storage/addresses/ash.meta.json" in repo "rq-filter" with data '{"city":"London","count":12}' updated "2025-01-03T00:00:00Z" and parent "${head2}"
    And the response status should be 200
    When I GET "/api/repos/rq-filter/records/addresses?where=city=London&sort=-count&limit=1"
    Then the response status should be 200
    And the records response contains 1 records
    And the records response record 0 has data field "count" equal to "12"
