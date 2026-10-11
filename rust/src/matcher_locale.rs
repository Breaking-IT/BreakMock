// Native OS locale discovery shared by date matchers and template helpers.
use std::sync::OnceLock;
static LOCALE: OnceLock<String> = OnceLock::new();
pub fn default_locale() -> String {
    #[cfg(test)]
    if let Some(locale) = TEST_LOCALE.with(|v| v.borrow().clone()) {
        return locale;
    }

    LOCALE
        .get_or_init(|| {
            #[cfg(target_os = "macos")]
            if let Some(locale) = apple_locale() {
                return locale;
            }
            for key in ["LC_ALL", "LC_TIME", "LANG"] {
                if let Ok(locale) = std::env::var(key) {
                    if !locale.is_empty() {
                        let locale = locale
                            .split('.')
                            .next()
                            .unwrap_or("")
                            .split('@')
                            .next()
                            .unwrap_or("");
                        return if locale == "C" || locale == "POSIX" {
                            "en_US".into()
                        } else {
                            locale.into()
                        };
                    }
                }
            }
            "en_US".into()
        })
        .clone()
}
#[cfg(target_os = "macos")]
fn apple_locale() -> Option<String> {
    use std::ffi::{CStr, c_char, c_void};
    #[link(name = "CoreFoundation", kind = "framework")]
    unsafe extern "C" {
        fn CFLocaleCopyCurrent() -> *const c_void;
        fn CFLocaleGetIdentifier(locale: *const c_void) -> *const c_void;
        fn CFStringGetCString(
            string: *const c_void,
            buffer: *mut c_char,
            size: isize,
            encoding: u32,
        ) -> u8;
        fn CFRelease(object: *const c_void);
    }
    unsafe {
        let locale = CFLocaleCopyCurrent();
        if locale.is_null() {
            return None;
        }
        let id = CFLocaleGetIdentifier(locale);
        let mut buffer = [0i8; 256];
        let ok =
            CFStringGetCString(id, buffer.as_mut_ptr(), buffer.len() as isize, 0x08000100) != 0;
        let result = if ok {
            Some(
                CStr::from_ptr(buffer.as_ptr())
                    .to_string_lossy()
                    .split('@')
                    .next()
                    .unwrap_or("en_US")
                    .to_owned(),
            )
        } else {
            None
        };
        CFRelease(locale);
        result
    }
}

#[cfg(test)]
thread_local! {static TEST_LOCALE:std::cell::RefCell<Option<String>>=const {std::cell::RefCell::new(None)};}
#[cfg(test)]
pub(super) struct TestLocale(Option<String>);
#[cfg(test)]
impl TestLocale {
    pub(super) fn new(locale: &str) -> Self {
        Self(TEST_LOCALE.with(|v| v.replace(Some(locale.into()))))
    }
}
#[cfg(test)]
impl Drop for TestLocale {
    fn drop(&mut self) {
        TEST_LOCALE.with(|v| {
            v.replace(self.0.take());
        });
    }
}
