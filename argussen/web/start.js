// The Android app opens the screens from file://: then its default server (standard.js, only in the Android build)
// is loaded before app.js.
if (!/^https?:$/.test(location.protocol)) document.write('<script src="standard.js"><\/script>');
