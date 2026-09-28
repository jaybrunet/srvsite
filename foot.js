<script>

// Send one JSON key/value pair to the server's /form endpoint.
// The part of the key after the last underscore is the "category"
// the server switches on (see handleForm in srvsite.go).
function sendForm(key, value) {
	return fetch('form', {
		method: 'POST',
		headers: {'Content-Type': 'application/json'},
		body: JSON.stringify({[key]: value.toString()})
	});
}

$(function() { // jQuery is ready

	// ---- Client-key login ------------------------------------------------
	$("#license-submit-input").click(async function() {
		var ok = true;
		if ($("#license-input").val().length < 35) {
			$("#license-input+div").show().delay(3333).fadeOut();
			ok = false;
		}
		if (ok) {
			var resp = await sendForm("chat_0_login", $("#license-input").val());
			var body = await resp.text();
			// The server sets the session cookie; only redirect on success.
			if (body.trim() == "ok") {
				window.location.href = "studio";
			} else {
				$("#license-input+div").show().delay(3333).fadeOut();
			}
		}
	});

	// Ignores the Enter key in the small text inputs.
	$(".pj-input").on("keydown", function(event) {
		if (event.which === 13) {
			event.preventDefault();
		}
	});

	// ---- Generic contact form -------------------------------------------
	$("#contact-submit").click(function() {
		var name = $("#contact-name").val().trim();
		var email = $("#contact-email").val().trim();
		var message = $("#contact-message").val().trim();

		if (name.length < 1 || email.length < 3 || message.length < 2) {
			$("#contact-done").hide();
			return;
		}

		var data = JSON.stringify({ name: name, email: email, message: message });
		sendForm("srvsite_contact", data);
		$("#contact-done").show().delay(4000).fadeOut();
		$("#contact-name, #contact-email, #contact-message").val("");
	});

	// ---- Scale-to-fit layout --------------------------------------------
	$(window).resize(function() {
		var width = document.getElementById("top").offsetWidth;
		var windowWidth = document.documentElement.clientWidth;
		if (windowWidth > 1400) { windowWidth = 1400; }
		var r = windowWidth / width;
		if (r < 1) {
			document.body.style.transformOrigin = 'top left';
		} else if (r > 1) {
			document.body.style.transformOrigin = 'top center';
			document.body.style.overflowX = 'hidden';
		}
		document.body.style.transform = 'scale(' + r + ')';
	});

	// Activate the transform scaler for the first pageview.
	$(window).trigger('resize');

}); // end jQuery Ready

</script>
