# Controller setup code fixtures

`valid.code` is a setup code exactly as the Pi encodes it (base64url, no padding, of the JSON bundle
returned by `POST /api/v1/sites/{siteId}/units`). The password inside is a fixture, not a credential.
The edge test (`TestSetupCodeFixtureMatchesTheEdgeEncoder`) proves the Pi produces this exact string,
and the firmware host tests prove the controller decodes it, so the two cannot drift apart.
