---
description: Build an iPhone app on this Mac and upload it to App Store Connect with the App Store Connect key.
applies: work tagged build, or uploading an app build to App Store Connect or TestFlight
---

# When to use
A work item from the App Store plugin ("Build … and upload it"), filed when the
app's Builds come from setting is "An agent on my Mac". It names the bundle ID,
branch, key ID and issuer ID, and usually the version and build number. The private key is a
file on this Mac; it's never in the item.

# How
1. Work on a fresh checkout of the branch the item names (main if it names none). Pull first.
2. Check the key file is there: `ls ~/.appstoreconnect/private_keys/AuthKey_<KEY_ID>.p8`.
   If it isn't, stop and hand the item back saying so: the person has to copy it there.
3. If the repo uses XcodeGen (a `project.yml`), run `xcodegen generate` first.
4. Find the scheme: `xcodebuild -list`. Use the app's scheme, not a test one.
5. Archive, with the version and build number from the item (leave out the ones it
   doesn't give), on the command line:

   ```sh
   xcodebuild archive \
     -scheme <Scheme> -destination 'generic/platform=iOS' \
     -archivePath build/App.xcarchive \
     -allowProvisioningUpdates \
     -authenticationKeyPath ~/.appstoreconnect/private_keys/AuthKey_<KEY_ID>.p8 \
     -authenticationKeyID <KEY_ID> -authenticationKeyIssuerID <ISSUER_ID> \
     MARKETING_VERSION=<version> CURRENT_PROJECT_VERSION=<build>
   ```

6. Upload it. Write `build/ExportOptions.plist` with `method` set to
   `app-store-connect`, `destination` set to `upload` and `signingStyle`
   `automatic`, then:

   ```sh
   xcodebuild -exportArchive \
     -archivePath build/App.xcarchive -exportPath build/export \
     -exportOptionsPlist build/ExportOptions.plist \
     -allowProvisioningUpdates \
     -authenticationKeyPath ~/.appstoreconnect/private_keys/AuthKey_<KEY_ID>.p8 \
     -authenticationKeyID <KEY_ID> -authenticationKeyIssuerID <ISSUER_ID>
   ```

7. Don't commit the version or build number, or anything in `build/`.

# If it fails
- "No signing certificate" or "No profiles": `-allowProvisioningUpdates` with the
  key makes them. The key needs the App Manager or Admin role. If it still fails, Xcode
  isn't signed in to the Apple account: hand back and say so.
- "The bundle version must be higher": the build number was used. Add one and try once more.
- Anything else: hand back with the last 20 lines of the error.

# Check before handing over
- The upload finished ("EXPORT SUCCEEDED" or "Upload succeeded").
- Note on the item: the version, build number and commit you built. Enplace takes it
  from there: TestFlight gives the build to the testers and writes What to test.
