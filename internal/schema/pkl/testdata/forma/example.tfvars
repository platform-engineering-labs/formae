region         = "us-west-2"
instance_count = 3
enable_logging = true
instance_type  = "t3.micro"

tags = {
  Name        = "web-server"
  Environment = "production"
}

availability_zones = ["us-west-2a", "us-west-2b", "us-west-2c"]
