# TestFlight: setting it up

These steps get your app from Enplace onto your own iPhone. Do them once per
app (part 1 is once for all your apps). Each step ticks itself off in the
TestFlight settings in Enplace as soon as Enplace can see that it's done.

You need: a Mac, an iPhone, and a paid Apple Developer account.

## Part 1. Make a key so Enplace can talk to Apple (once)

1. On your Mac, open **appstoreconnect.apple.com** and sign in.
2. Click **Users and Access** at the top.
3. Click the **Integrations** tab, then **App Store Connect API** on the left.
   The first time, Apple asks you to **Request Access**. Say yes.
4. Under **Team Keys**, click the blue **+** button.
5. Name it `Enplace`. For **Access**, pick **App Manager**. Click **Generate**.
6. Click **Download** next to your new key. A file called something like
   `AuthKey_ABC123XYZ.p8` goes into your Downloads folder.
   Apple only lets you download it **once**, so keep it safe.
7. In Enplace, open the **TestFlight** settings on your app (or the App Store
   settings: it's the same key) and fill in:
   - **Issuer ID**: the long code above the list of keys on Apple's page.
   - **Key ID**: the short code in your key's row.
   - **Private key**: open the `.p8` file with TextEdit, select everything, copy
     it and paste it in.

## Part 2. Tell Apple about your app

1. In Xcode, click your app at the top of the list on the left, then **General**.
   Copy the **Bundle Identifier** (like `com.guedio.planit`).
2. In Enplace, paste it into the **App Store** settings of your app, under
   **App Store app**.
3. On **appstoreconnect.apple.com**, click **Apps**, then the blue **+**, then
   **New App**.
4. Fill it in:
   - **Platforms**: iOS.
   - **Name**: your app's name. If Apple says it's taken, add a word.
   - **Primary Language**: yours.
   - **Bundle ID**: pick the one from step 1. Not in the list? Open the app in
     Xcode, tick **Automatically manage signing** under **Signing & Capabilities**,
     pick your team, wait a moment, then reload this page.
   - **SKU**: any short name only you see, like `planit-ios`.
   - **User Access**: Full Access.
5. Click **Create**.

## Part 3. Get the builds on your iPhone

1. On your iPhone, open the App Store, search for **TestFlight** (it's made by
   Apple) and install it. Sign in with the same Apple ID as App Store Connect.
2. In Enplace's TestFlight settings, type that email into **Who gets every build**.
3. Enplace makes a private tester group called **Me** and adds you. You get an
   email: **"You're invited to test …"**. Open it on your iPhone, tap
   **View in TestFlight**, then **Accept**.
4. From now on, every new build shows up in TestFlight. Tap **Install** (or
   **Update**).

## Part 4. Pick who makes the builds

Choose one under **Who makes builds** in the TestFlight settings.

### Xcode Cloud (Apple builds it)

1. In Enplace, switch on the **Xcode Cloud** plugin on the same app.
2. Open your app in Xcode on your Mac.
3. In the menu bar choose **Product → Xcode Cloud → Create Workflow**. Pick your
   app and click **Next**.
4. Xcode shows a workflow called **Default**. Click **Edit Workflow**:
   - Under **Actions**, open **Archive - iOS** and set **Deployment Preparation**
     to **TestFlight (Internal Testing Only)**.
   - Under **Post-Actions**, click **+**, choose **TestFlight Internal Testing**
     and add the group **Me**.
   - Click **Save**.
5. Xcode asks to connect to GitHub. Click **Grant Access**, sign in to GitHub in
   the browser, and allow only your app's repo. Go back to Xcode and click
   **Complete**.
6. Xcode Cloud starts its first build. In Enplace, the Xcode Cloud steps tick off,
   and **Send to TestFlight** on the app starts a new build whenever you like.

If your repo makes its Xcode project with XcodeGen (it has a `project.yml` and no
`.xcodeproj`), Xcode Cloud needs a small script to make it first. Ask an agent to
"add a ci_scripts/ci_post_clone.sh that installs XcodeGen and runs xcodegen".

### An agent on your Mac

1. Open **Terminal** on the Mac where your agents run and paste this, then press
   Return. It makes the folder Apple's tools look in:
   `mkdir -p ~/.appstoreconnect/private_keys`
2. In Finder choose **Go → Go to Folder…**, type `~/.appstoreconnect/private_keys`
   and press Return. Drag your `AuthKey_….p8` file from Downloads into it.
3. Open Xcode, choose **Xcode → Settings → Accounts**, click **+**, choose
   **Apple ID** and sign in.
4. Press **Send to TestFlight** on the app in Enplace. An agent builds the app and
   uploads it. It takes about 10 minutes, then Apple processes it for about 10 more.

### You, from Xcode

In Xcode choose **Product → Archive**. When it's done, click **Distribute App**,
then **TestFlight Internal Only**, then **Distribute**. Enplace takes it from there.

## The encryption question

Apple asks about every build whether it uses encryption. Most apps only use what's
built into iOS (like https), which counts as **no**. When a build waits on it, press
**It uses no special encryption** on the TestFlight card. To never be asked, have an
agent add `ITSAppUsesNonExemptEncryption` = `NO` to the app's Info.plist.
